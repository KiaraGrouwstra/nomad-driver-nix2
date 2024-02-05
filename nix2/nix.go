package nix2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/hashicorp/nomad/helper/pluginutils/hclutils"
)

const (
	closureNix = `
{ path }:
let
  nixpkgs = builtins.getFlake "%s";
  inherit (nixpkgs.legacyPackages.x86_64-linux) buildPackages;
in buildPackages.closureInfo { rootPaths = builtins.storePath path; }
`
)

func prepareNixPackages(taskDir string, packages []string, nixpkgs string) (hclutils.MapStrStr, error) {
	mounts := make(hclutils.MapStrStr)

	profileLink := filepath.Join(taskDir, "current-profile")
	profile, err := nixBuildProfile(taskDir, packages, profileLink)
	if err != nil {
		return nil, fmt.Errorf("Build of the flakes failed: %v", err)
	}

	closureLink := filepath.Join(taskDir, "current-closure")
	closure, err := nixBuildClosure(profileLink, closureLink, nixpkgs)
	if err != nil {
		return nil, fmt.Errorf("Build of the flakes failed: %v", err)
	}

	mounts[profile] = profile

	if entries, err := os.ReadDir(profile); err != nil {
		return nil, fmt.Errorf("Couldn't read profile directory: %w", err)
	} else {
		for _, entry := range entries {
			if name := entry.Name(); name != "etc" {
				mounts[filepath.Join(profile, name)] = "/" + name
				continue
			}

			etcEntries, err := os.ReadDir(filepath.Join(profile, "etc"))
			if err != nil {
				return nil, fmt.Errorf("Couldn't read profile's /etc directory: %w", err)
			}

			for _, etcEntry := range etcEntries {
				etcName := etcEntry.Name()
				mounts[filepath.Join(profile, "etc", etcName)] = "/etc/" + etcName
			}
		}
	}

	requisites, err := nixRequisites(closure)
	if err != nil {
		return nil, fmt.Errorf("Couldn't determine flake requisites: %v", err)
	}

	for _, requisite := range requisites {
		mounts[requisite] = requisite
	}

	return mounts, nil
}

func nixBuildProfile(taskDir string, flakes []string, link string) (string, error) {
	cmd := exec.Command("nix", append(
		[]string{
			"--extra-experimental-features", "nix-command",
			"--extra-experimental-features", "flakes",
			"profile",
			"install",
			"--no-write-lock-file",
			"--profile",
			link},
		flakes...)...)
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	cmd.Dir = taskDir

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v failed: %s. Err: %v", cmd.Args, stderr.String(), err)
	}

	if target, err := os.Readlink(link); err == nil {
		return os.Readlink(filepath.Join(filepath.Dir(link), target))
	} else {
		return "", err
	}
}

func nixBuildClosure(profile string, link string, nixpkgs string) (string, error) {
	cmd := exec.Command(
		"nix",
		"--extra-experimental-features", "nix-command",
		"--extra-experimental-features", "flakes",
		"build",
		"--out-link", link,
		"--expr", fmt.Sprintf(closureNix, nixpkgs),
		"--impure",
		"--no-write-lock-file",
		"--argstr", "path", profile)

	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v failed: %s. Err: %v", cmd.Args, stderr.String(), err)
	}

	return os.Readlink(link)
}

type nixPathInfo struct {
	NarHash          string   `json:"narHash"`
	NarSize          uint64   `json:"narSize"`
	References       []string `json:"references"`
	Deriver          string   `json:"deriver"`
	RegistrationTime uint64   `json:"registrationTime"`
	Signatures       []string `json:"signatures"`
}

func nixRequisites(path string) ([]string, error) {
	cmd := exec.Command(
		"nix",
		"--extra-experimental-features", "nix-command",
		"--extra-experimental-features", "flakes",
		"path-info",
		"--json",
		"--recursive",
		path)

	stdout := &bytes.Buffer{}
	cmd.Stdout = stdout

	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v failed: %s. Err: %v", cmd.Args, stderr.String(), err)
	}

	result := []*nixPathInfo{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, err
	}

	requisites := []string{}
	for _, result := range result {
		requisites = append(requisites, result.Path)
	}

	return requisites, nil
}
