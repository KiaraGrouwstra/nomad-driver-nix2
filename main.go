package main

import (
	exec2 "github.com/Alexis211/nomad-driver-exec2/exec2"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/plugins"
)

func main() {
	// Serve the plugin
	plugins.Serve(factory)
}

// factory returns a new instance of a nomad driver plugin
func factory(log hclog.Logger) interface{} {
	return exec2.NewPlugin(log)
}
