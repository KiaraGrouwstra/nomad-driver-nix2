package nix2

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/Alexis211/nomad-driver-exec2/executor"
	"github.com/hashicorp/consul-template/signals"
	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/lib/cgutil"
	"github.com/hashicorp/nomad/drivers/shared/capabilities"
	"github.com/hashicorp/nomad/drivers/shared/eventer"
	"github.com/hashicorp/nomad/drivers/shared/resolvconf"
	"github.com/hashicorp/nomad/helper/pluginutils/hclutils"
	"github.com/hashicorp/nomad/helper/pluginutils/loader"
	"github.com/hashicorp/nomad/helper/pointer"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/drivers/utils"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	pstructs "github.com/hashicorp/nomad/plugins/shared/structs"
)

const (
	// pluginName is the name of the plugin
	pluginName = "nix2"

	// fingerprintPeriod is the interval at which the driver will send fingerprint responses
	fingerprintPeriod = 30 * time.Second

	// taskHandleVersion is the version of task handle which this driver sets
	// and understands how to decode driver state
	taskHandleVersion = 1
)

var (
	// PluginID is the exec plugin metadata registered in the plugin
	// catalog.
	PluginID = loader.PluginID{
		Name:       pluginName,
		PluginType: base.PluginTypeDriver,
	}

	// pluginInfo is the response returned for the PluginInfo RPC
	pluginInfo = &base.PluginInfoResponse{
		Type:              base.PluginTypeDriver,
		PluginApiVersions: []string{drivers.ApiVersion010},
		PluginVersion:     "0.1.0",
		Name:              pluginName,
	}

	// configSpec is the hcl specification returned by the ConfigSchema RPC
	configSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		"no_pivot_root": hclspec.NewDefault(
			hclspec.NewAttr("no_pivot_root", "bool", false),
			hclspec.NewLiteral("false"),
		),
		"default_pid_mode": hclspec.NewDefault(
			hclspec.NewAttr("default_pid_mode", "string", false),
			hclspec.NewLiteral(`"private"`),
		),
		"default_ipc_mode": hclspec.NewDefault(
			hclspec.NewAttr("default_ipc_mode", "string", false),
			hclspec.NewLiteral(`"private"`),
		),
		"allow_caps": hclspec.NewDefault(
			hclspec.NewAttr("allow_caps", "list(string)", false),
			hclspec.NewLiteral(capabilities.HCLSpecLiteral),
		),
		"allow_bind": hclspec.NewDefault(
			hclspec.NewAttr("allow_bind", "bool", false),
			hclspec.NewLiteral("true"),
		),
	})

	// taskConfigSpec is the hcl specification for the driver config section of
	// a task within a job. It is returned in the TaskConfigSchema RPC
	taskConfigSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		"command":        hclspec.NewAttr("command", "string", true),
		"args":           hclspec.NewAttr("args", "list(string)", false),
		"bind":           hclspec.NewAttr("bind", "list(map(string))", false),
		"bind_read_only": hclspec.NewAttr("bind_read_only", "list(map(string))", false),
		"pid_mode":       hclspec.NewAttr("pid_mode", "string", false),
		"ipc_mode":       hclspec.NewAttr("ipc_mode", "string", false),
		"cap_add":        hclspec.NewAttr("cap_add", "list(string)", false),
		"cap_drop":       hclspec.NewAttr("cap_drop", "list(string)", false),
	})

	// driverCapabilities represents the RPC response for what features are
	// implemented by the exec task driver
	driverCapabilities = &drivers.Capabilities{
		SendSignals: true,
		Exec:        true,
		FSIsolation: drivers.FSIsolationNone,
		NetIsolationModes: []drivers.NetIsolationMode{
			drivers.NetIsolationModeHost,
			drivers.NetIsolationModeGroup,
		},
		MountConfigs: drivers.MountConfigSupportAll,
	}
)

// Driver fork/execs tasks using many of the underlying OS's isolation
// features where configured.
type Driver struct {
	// eventer is used to handle multiplexing of TaskEvents calls such that an
	// event can be broadcast to all callers
	eventer *eventer.Eventer

	// config is the driver configuration set by the SetConfig RPC
	config Config

	// tasks is the in memory datastore mapping taskIDs to driverHandles
	tasks *taskStore

	// ctx is the context for the driver. It is passed to other subsystems to
	// coordinate shutdown
	ctx context.Context

	// signalShutdown is called when the driver is shutting down and cancels
	// the ctx passed to any subsystems
	signalShutdown context.CancelFunc

	// logger will log to the Nomad agent
	logger hclog.Logger

	// A tri-state boolean to know if the fingerprinting has happened and
	// whether it has been successful
	fingerprintSuccess *bool
	fingerprintLock    sync.Mutex
}

// Config is the driver configuration set by the SetConfig RPC call
type Config struct {
	// NoPivotRoot disables the use of pivot_root, useful when the root partition
	// is on ramdisk
	NoPivotRoot bool `codec:"no_pivot_root"`

	// DefaultModePID is the default PID isolation set for all tasks using
	// exec-based task drivers.
	DefaultModePID string `codec:"default_pid_mode"`

	// DefaultModeIPC is the default IPC isolation set for all tasks using
	// exec-based task drivers.
	DefaultModeIPC string `codec:"default_ipc_mode"`

	// AllowCaps configures which Linux Capabilities are enabled for tasks
	// running on this node.
	AllowCaps []string `codec:"allow_caps"`

	// AllowBind defines whether users may bind host directories
	AllowBind bool `codec:"allow_bind"`
}

func (c *Config) validate() error {
	switch c.DefaultModePID {
	case executor.IsolationModePrivate, executor.IsolationModeHost:
	default:
		return fmt.Errorf("default_pid_mode must be %q or %q, got %q", executor.IsolationModePrivate, executor.IsolationModeHost, c.DefaultModePID)
	}

	switch c.DefaultModeIPC {
	case executor.IsolationModePrivate, executor.IsolationModeHost:
	default:
		return fmt.Errorf("default_ipc_mode must be %q or %q, got %q", executor.IsolationModePrivate, executor.IsolationModeHost, c.DefaultModeIPC)
	}

	badCaps := capabilities.Supported().Difference(capabilities.New(c.AllowCaps))
	if !badCaps.Empty() {
		return fmt.Errorf("allow_caps configured with capabilities not supported by system: %s", badCaps)
	}

	return nil
}

// TaskConfig is the driver configuration of a task within a job
type TaskConfig struct {
	// Command is the thing to exec.
	Command string `codec:"command"`

	// Args are passed along to Command.
	Args []string `codec:"args"`

	// Paths to bind for read-write acess
	Bind hclutils.MapStrStr `codec:"bind"`

	// Paths to bind for read-only acess
	BindReadOnly hclutils.MapStrStr `codec:"bind_read_only"`

	// ModePID indicates whether PID namespace isolation is enabled for the task.
	// Must be "private" or "host" if set.
	ModePID string `codec:"pid_mode"`

	// ModeIPC indicates whether IPC namespace isolation is enabled for the task.
	// Must be "private" or "host" if set.
	ModeIPC string `codec:"ipc_mode"`

	// CapAdd is a set of linux capabilities to enable.
	CapAdd []string `codec:"cap_add"`

	// CapDrop is a set of linux capabilities to disable.
	CapDrop []string `codec:"cap_drop"`
}

func (tc *TaskConfig) validate() error {
	switch tc.ModePID {
	case "", executor.IsolationModePrivate, executor.IsolationModeHost:
	default:
		return fmt.Errorf("pid_mode must be %q or %q, got %q", executor.IsolationModePrivate, executor.IsolationModeHost, tc.ModePID)
	}

	switch tc.ModeIPC {
	case "", executor.IsolationModePrivate, executor.IsolationModeHost:
	default:
		return fmt.Errorf("ipc_mode must be %q or %q, got %q", executor.IsolationModePrivate, executor.IsolationModeHost, tc.ModeIPC)
	}

	supported := capabilities.Supported()
	badAdds := supported.Difference(capabilities.New(tc.CapAdd))
	if !badAdds.Empty() {
		return fmt.Errorf("cap_add configured with capabilities not supported by system: %s", badAdds)
	}
	badDrops := supported.Difference(capabilities.New(tc.CapDrop))
	if !badDrops.Empty() {
		return fmt.Errorf("cap_drop configured with capabilities not supported by system: %s", badDrops)
	}

	return nil
}

// TaskState is the state which is encoded in the handle returned in
// StartTask. This information is needed to rebuild the task state and handler
// during recovery.
type TaskState struct {
	TaskConfig *drivers.TaskConfig
	Pid        int
	StartedAt  time.Time
}

// NewPlugin returns a new DrivePlugin implementation
func NewPlugin(logger hclog.Logger) drivers.DriverPlugin {
	ctx, cancel := context.WithCancel(context.Background())
	logger = logger.Named(pluginName)
	return &Driver{
		eventer:        eventer.NewEventer(ctx, logger),
		tasks:          newTaskStore(),
		ctx:            ctx,
		signalShutdown: cancel,
		logger:         logger,
	}
}

// setFingerprintSuccess marks the driver as having fingerprinted successfully
func (d *Driver) setFingerprintSuccess() {
	d.fingerprintLock.Lock()
	d.fingerprintSuccess = pointer.Of(true)
	d.fingerprintLock.Unlock()
}

// setFingerprintFailure marks the driver as having failed fingerprinting
func (d *Driver) setFingerprintFailure() {
	d.fingerprintLock.Lock()
	d.fingerprintSuccess = pointer.Of(false)
	d.fingerprintLock.Unlock()
}

// fingerprintSuccessful returns true if the driver has
// never fingerprinted or has successfully fingerprinted
func (d *Driver) fingerprintSuccessful() bool {
	d.fingerprintLock.Lock()
	defer d.fingerprintLock.Unlock()
	return d.fingerprintSuccess == nil || *d.fingerprintSuccess
}

func (d *Driver) PluginInfo() (*base.PluginInfoResponse, error) {
	return pluginInfo, nil
}

func (d *Driver) ConfigSchema() (*hclspec.Spec, error) {
	return configSpec, nil
}

func (d *Driver) SetConfig(cfg *base.Config) error {
	// unpack, validate, and set agent plugin config
	var config Config
	if len(cfg.PluginConfig) != 0 {
		if err := base.MsgPackDecode(cfg.PluginConfig, &config); err != nil {
			return err
		}
	}
	if err := config.validate(); err != nil {
		return err
	}
	d.logger.Info("Got config", "driver_config", hclog.Fmt("%+v", config))
	d.config = config

	return nil
}

func (d *Driver) TaskConfigSchema() (*hclspec.Spec, error) {
	return taskConfigSpec, nil
}

// Capabilities is returned by the Capabilities RPC and indicates what
// optional features this driver supports
func (d *Driver) Capabilities() (*drivers.Capabilities, error) {
	return driverCapabilities, nil
}

func (d *Driver) Fingerprint(ctx context.Context) (<-chan *drivers.Fingerprint, error) {
	ch := make(chan *drivers.Fingerprint)
	go d.handleFingerprint(ctx, ch)
	return ch, nil

}
func (d *Driver) handleFingerprint(ctx context.Context, ch chan<- *drivers.Fingerprint) {
	defer close(ch)
	ticker := time.NewTimer(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			ticker.Reset(fingerprintPeriod)
			ch <- d.buildFingerprint()
		}
	}
}

func (d *Driver) buildFingerprint() *drivers.Fingerprint {
	if runtime.GOOS != "linux" {
		d.setFingerprintFailure()
		return &drivers.Fingerprint{
			Health:            drivers.HealthStateUndetected,
			HealthDescription: "exec driver unsupported on client OS",
		}
	}

	fp := &drivers.Fingerprint{
		Attributes:        map[string]*pstructs.Attribute{},
		Health:            drivers.HealthStateHealthy,
		HealthDescription: drivers.DriverHealthy,
	}

	if !utils.IsUnixRoot() {
		fp.Health = drivers.HealthStateUndetected
		fp.HealthDescription = drivers.DriverRequiresRootMessage
		d.setFingerprintFailure()
		return fp
	}

	mount, err := cgutil.FindCgroupMountpointDir()
	if err != nil {
		fp.Health = drivers.HealthStateUnhealthy
		fp.HealthDescription = drivers.NoCgroupMountMessage
		if d.fingerprintSuccessful() {
			d.logger.Warn(fp.HealthDescription, "error", err)
		}
		d.setFingerprintFailure()
		return fp
	}

	if mount == "" {
		fp.Health = drivers.HealthStateUnhealthy
		fp.HealthDescription = drivers.CgroupMountEmpty
		d.setFingerprintFailure()
		return fp
	}

	fp.Attributes["driver.exec"] = pstructs.NewBoolAttribute(true)
	d.setFingerprintSuccess()
	return fp
}

func (d *Driver) RecoverTask(handle *drivers.TaskHandle) error {
	if handle == nil {
		return fmt.Errorf("handle cannot be nil")
	}

	// If already attached to handle there's nothing to recover.
	if _, ok := d.tasks.Get(handle.Config.ID); ok {
		d.logger.Trace("nothing to recover; task already exists",
			"task_id", handle.Config.ID,
			"task_name", handle.Config.Name,
		)
		return nil
	}

	// Handle doesn't already exist, try to reattach
	var taskState TaskState
	if err := handle.GetDriverState(&taskState); err != nil {
		d.logger.Error("failed to decode task state from handle", "error", err, "task_id", handle.Config.ID)
		return fmt.Errorf("failed to decode task state from handle: %v", err)
	}

	// Create new executor
	exec := executor.NewExecutorWithIsolation(
		d.logger.With("task_name", handle.Config.Name, "alloc_id", handle.Config.AllocID))

	h := &taskHandle{
		exec:       exec,
		pid:        taskState.Pid,
		taskConfig: taskState.TaskConfig,
		procState:  drivers.TaskStateRunning,
		startedAt:  taskState.StartedAt,
		exitResult: &drivers.ExitResult{},
		logger:     d.logger,
	}

	d.tasks.Set(taskState.TaskConfig.ID, h)

	go h.run()
	return nil
}

func (d *Driver) StartTask(cfg *drivers.TaskConfig) (*drivers.TaskHandle, *drivers.DriverNetwork, error) {
	if _, ok := d.tasks.Get(cfg.ID); ok {
		return nil, nil, fmt.Errorf("task with ID %q already started", cfg.ID)
	}

	var driverConfig TaskConfig
	if err := cfg.DecodeDriverConfig(&driverConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to decode driver config: %v", err)
	}

	if err := driverConfig.validate(); err != nil {
		return nil, nil, fmt.Errorf("failed driver config validation: %v", err)
	}

	d.logger.Info("starting task", "driver_cfg", hclog.Fmt("%+v", driverConfig))
	handle := drivers.NewTaskHandle(taskHandleVersion)
	handle.Config = cfg

	exec := executor.NewExecutorWithIsolation(
		d.logger.With("task_name", handle.Config.Name, "alloc_id", handle.Config.AllocID))

	user := cfg.User
	if user == "" {
		user = "0"
	}

	if cfg.DNS != nil {
		dnsMount, err := resolvconf.GenerateDNSMount(cfg.TaskDir().Dir, cfg.DNS)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build mount for resolv.conf: %v", err)
		}
		cfg.Mounts = append(cfg.Mounts, dnsMount)
	}

	// Bind mounts specified in driver config

	// Bind mounts specified in task config
	if d.config.AllowBind {
		if driverConfig.Bind != nil {
			for host, task := range driverConfig.Bind {
				mount_config := drivers.MountConfig{
					TaskPath:        task,
					HostPath:        host,
					Readonly:        false,
					PropagationMode: "private",
				}
				d.logger.Info("adding RW mount from task spec", "mount_config", hclog.Fmt("%+v", mount_config))
				cfg.Mounts = append(cfg.Mounts, &mount_config)
			}
		}
		if driverConfig.BindReadOnly != nil {
			for host, task := range driverConfig.BindReadOnly {
				mount_config := drivers.MountConfig{
					TaskPath:        task,
					HostPath:        host,
					Readonly:        true,
					PropagationMode: "private",
				}
				d.logger.Info("adding RO mount from task spec", "mount_config", hclog.Fmt("%+v", mount_config))
				cfg.Mounts = append(cfg.Mounts, &mount_config)
			}
		}
	} else {
		if len(driverConfig.Bind) > 0 || len(driverConfig.BindReadOnly) > 0 {
			return nil, nil, fmt.Errorf("bind and bind_read_only are deactivated for the %s driver", pluginName)
		}
	}

	caps, err := capabilities.Calculate(
		capabilities.NomadDefaults(), d.config.AllowCaps, driverConfig.CapAdd, driverConfig.CapDrop,
	)
	if err != nil {
		return nil, nil, err
	}
	d.logger.Debug("task capabilities", "capabilities", caps)

	execCmd := &executor.ExecCommand{
		Cmd:              driverConfig.Command,
		Args:             driverConfig.Args,
		Env:              cfg.EnvList(),
		User:             user,
		ResourceLimits:   true,
		NoPivotRoot:      d.config.NoPivotRoot,
		Resources:        cfg.Resources,
		TaskDir:          cfg.TaskDir().Dir,
		StdoutPath:       cfg.StdoutPath,
		StderrPath:       cfg.StderrPath,
		Mounts:           cfg.Mounts,
		Devices:          cfg.Devices,
		NetworkIsolation: cfg.NetworkIsolation,
		ModePID:          executor.IsolationMode(d.config.DefaultModePID, driverConfig.ModePID),
		ModeIPC:          executor.IsolationMode(d.config.DefaultModeIPC, driverConfig.ModeIPC),
		Capabilities:     caps,
	}

	d.logger.Info("launching with", "exec_cmd", hclog.Fmt("%+v", execCmd))

	ps, err := exec.Launch(execCmd)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to launch command with executor: %v", err)
	}

	h := &taskHandle{
		exec:       exec,
		pid:        ps.Pid,
		taskConfig: cfg,
		procState:  drivers.TaskStateRunning,
		startedAt:  time.Now().Round(time.Millisecond),
		logger:     d.logger,
	}

	driverState := TaskState{
		Pid:        ps.Pid,
		TaskConfig: cfg,
		StartedAt:  h.startedAt,
	}

	if err := handle.SetDriverState(&driverState); err != nil {
		d.logger.Error("failed to start task, error setting driver state", "error", err)
		_ = exec.Shutdown("", 0)
		return nil, nil, fmt.Errorf("failed to set driver state: %v", err)
	}

	d.tasks.Set(cfg.ID, h)
	go h.run()
	return handle, nil, nil
}

func (d *Driver) WaitTask(ctx context.Context, taskID string) (<-chan *drivers.ExitResult, error) {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	ch := make(chan *drivers.ExitResult)
	go d.handleWait(ctx, handle, ch)

	return ch, nil
}

func (d *Driver) handleWait(ctx context.Context, handle *taskHandle, ch chan *drivers.ExitResult) {
	defer close(ch)
	var result *drivers.ExitResult
	ps, err := handle.exec.Wait(ctx)
	if err != nil {
		result = &drivers.ExitResult{
			Err: fmt.Errorf("executor: error waiting on process: %v", err),
		}
	} else {
		result = &drivers.ExitResult{
			ExitCode: ps.ExitCode,
			Signal:   ps.Signal,
		}
	}

	select {
	case <-ctx.Done():
		return
	case <-d.ctx.Done():
		return
	case ch <- result:
	}
}

func (d *Driver) StopTask(taskID string, timeout time.Duration, signal string) error {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	if err := handle.exec.Shutdown(signal, timeout); err != nil {
		return fmt.Errorf("executor Shutdown failed: %v", err)
	}

	return nil
}

// resetCgroup will re-create the v2 cgroup for the task after the task has been
// destroyed by libcontainer. In the case of a task restart we call DestroyTask
// which removes the cgroup - but we still need it!
//
// Ideally the cgroup management would be more unified - and we could do the creation
// on a task runner pre-start hook, eliminating the need for this hack.
func (d *Driver) resetCgroup(handle *taskHandle) {
	if cgutil.UseV2 {
		if handle.taskConfig.Resources != nil &&
			handle.taskConfig.Resources.LinuxResources != nil &&
			handle.taskConfig.Resources.LinuxResources.CpusetCgroupPath != "" {
			err := os.Mkdir(handle.taskConfig.Resources.LinuxResources.CpusetCgroupPath, 0755)
			if err != nil {
				d.logger.Trace("failed to reset cgroup", "path", handle.taskConfig.Resources.LinuxResources.CpusetCgroupPath)
			}
		}
	}
}

func (d *Driver) DestroyTask(taskID string, force bool) error {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	if handle.IsRunning() && !force {
		return fmt.Errorf("cannot destroy running task")
	}

	if err := handle.exec.Shutdown("", 0); err != nil {
		handle.logger.Error("destroying executor failed", "error", err)
	}

	// workaround for the case where DestroyTask was issued on task restart
	d.resetCgroup(handle)

	d.tasks.Delete(taskID)
	return nil
}

func (d *Driver) InspectTask(taskID string) (*drivers.TaskStatus, error) {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	return handle.TaskStatus(), nil
}

func (d *Driver) TaskStats(ctx context.Context, taskID string, interval time.Duration) (<-chan *drivers.TaskResourceUsage, error) {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	return handle.exec.Stats(ctx, interval)
}

func (d *Driver) TaskEvents(ctx context.Context) (<-chan *drivers.TaskEvent, error) {
	return d.eventer.TaskEvents(ctx)
}

func (d *Driver) SignalTask(taskID string, signal string) error {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	sig := os.Interrupt
	if s, ok := signals.SignalLookup[signal]; ok {
		sig = s
	} else {
		d.logger.Warn("unknown signal to send to task, using SIGINT instead", "signal", signal, "task_id", handle.taskConfig.ID)

	}
	return handle.exec.Signal(sig)
}

func (d *Driver) ExecTask(taskID string, cmd []string, timeout time.Duration) (*drivers.ExecTaskResult, error) {
	if len(cmd) == 0 {
		return nil, fmt.Errorf("error cmd must have at least one value")
	}
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	args := []string{}
	if len(cmd) > 1 {
		args = cmd[1:]
	}

	out, exitCode, err := handle.exec.Exec(time.Now().Add(timeout), cmd[0], args)
	if err != nil {
		return nil, err
	}

	return &drivers.ExecTaskResult{
		Stdout: out,
		ExitResult: &drivers.ExitResult{
			ExitCode: exitCode,
		},
	}, nil
}

var _ drivers.ExecTaskStreamingRawDriver = (*Driver)(nil)

func (d *Driver) ExecTaskStreamingRaw(ctx context.Context,
	taskID string,
	command []string,
	tty bool,
	stream drivers.ExecTaskStream) error {

	if len(command) == 0 {
		return fmt.Errorf("error cmd must have at least one value")
	}
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	return handle.exec.ExecStreaming(ctx, command, tty, stream)
}