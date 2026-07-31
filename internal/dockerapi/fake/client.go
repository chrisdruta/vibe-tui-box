// Package fake is a programmable in-memory dockerapi.Client. It
// records every request verbatim so tests can assert full request
// equality, and it maintains a small world model (containers, volumes,
// networks) that behaves like a cooperative daemon.
package fake

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// Call is one recorded client invocation.
type Call struct {
	Method  string
	Request any
}

type Client struct {
	mu    sync.Mutex
	calls []Call

	// Scripted behavior. Zero values mean "succeed with defaults".
	PingErr        error
	ResolveResults map[dockerapi.ImageRef]dockerapi.ResolvedImage
	ResolveErrs    map[dockerapi.ImageRef]error
	PullErrs       map[dockerapi.ImageRef]error
	CreateErr      error
	// CreateErrOnce fails only the next CreateContainer call, then clears
	// itself. It models the create → ErrNotFound → pull → re-create
	// retry, which sticky CreateErr cannot express and CreateHook (which
	// runs only after a *successful* create) cannot reach.
	CreateErrOnce error
	// CreateHook runs after a successful create, letting tests emulate
	// container side effects (e.g. a dev build writing its output).
	CreateHook func(dockerapi.CreateRequest)
	StartErr   error
	StopErr    error
	RemoveErr  error
	RenameErr  error
	// Per-container failure hooks for transaction tests: each runs after
	// the target is located and before any mutation; a non-nil return
	// fails the call. Nil hooks mean "succeed with defaults".
	StartHookErr  func(dockerapi.ContainerState) error
	StopHookErr   func(dockerapi.ContainerState) error
	RemoveHookErr func(dockerapi.ContainerState) error
	RenameHookErr func(dockerapi.ContainerState, dockerapi.ContainerName) error
	// StartExits models containers that exit immediately after a
	// successful start: StartContainer returns nil but Running stays
	// false, like a crashing sidecar.
	StartExits map[dockerapi.ContainerID]bool
	// MintedIPs is the sticky per-container network address the fake
	// assigns on start (and clears on stop, like a real daemon that
	// reports no address for a stopped container). Auto-minted for
	// containers created with a network; seed a different value to
	// model address drift across a restart.
	MintedIPs   map[dockerapi.ContainerID]string
	WaitCodes   map[dockerapi.ContainerID]int
	ExecResults map[string]dockerapi.ExecResult // keyed by argv joined with \x00
	ExecOutputs map[string]string               // stdout written to Streams.Out, same key
	ExecErr     error
	LogsOutputs map[dockerapi.ContainerName]string
	LogsErr     error
	BuildResult dockerapi.BuiltImage
	BuildErr    error

	// World state, exported for direct inspection and seeding.
	Containers map[dockerapi.ContainerName]*dockerapi.ContainerState
	Volumes    map[string]dockerapi.VolumeSpec
	Networks   map[string]dockerapi.NetworkSpec

	nextID int
	nextIP int
	// createNetworks remembers each created container's network (keyed
	// by immutable ID — the .prev/.next rename dance changes names) so
	// StartContainer knows where to mint an address.
	createNetworks map[dockerapi.ContainerID]string
}

var _ dockerapi.Client = (*Client)(nil)

func New() *Client {
	return &Client{
		ResolveResults: map[dockerapi.ImageRef]dockerapi.ResolvedImage{},
		ResolveErrs:    map[dockerapi.ImageRef]error{},
		PullErrs:       map[dockerapi.ImageRef]error{},
		ExecResults:    map[string]dockerapi.ExecResult{},
		ExecOutputs:    map[string]string{},
		LogsOutputs:    map[dockerapi.ContainerName]string{},
		WaitCodes:      map[dockerapi.ContainerID]int{},
		MintedIPs:      map[dockerapi.ContainerID]string{},
		Containers:     map[dockerapi.ContainerName]*dockerapi.ContainerState{},
		Volumes:        map[string]dockerapi.VolumeSpec{},
		Networks:       map[string]dockerapi.NetworkSpec{},
		createNetworks: map[dockerapi.ContainerID]string{},
	}
}

// Calls returns the recorded invocations in order.
func (c *Client) Calls() []Call {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Call(nil), c.calls...)
}

// CallsTo filters recorded calls by method name.
func (c *Client) CallsTo(method string) []Call {
	var out []Call
	for _, call := range c.Calls() {
		if call.Method == method {
			out = append(out, call)
		}
	}
	return out
}

func (c *Client) record(method string, req any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, Call{Method: method, Request: req})
}

func (c *Client) Ping(ctx context.Context) error {
	c.record("Ping", nil)
	return c.PingErr
}

func (c *Client) ResolveImage(ctx context.Context, ref dockerapi.ImageRef) (dockerapi.ResolvedImage, error) {
	c.record("ResolveImage", ref)
	if err := c.ResolveErrs[ref]; err != nil {
		return dockerapi.ResolvedImage{}, err
	}
	if res, ok := c.ResolveResults[ref]; ok {
		return res, nil
	}
	// Default: deterministic digest derived from the reference.
	return dockerapi.ResolvedImage{Ref: ref, Digest: domain.SHA256([]byte(ref))}, nil
}

func (c *Client) PullImage(ctx context.Context, img dockerapi.ResolvedImage, sink dockerapi.ProgressSink) error {
	c.record("PullImage", img)
	return c.PullErrs[img.Ref]
}

func (c *Client) Build(ctx context.Context, req dockerapi.BuildRequest, sink dockerapi.ProgressSink) (dockerapi.BuiltImage, error) {
	c.record("Build", req)
	return c.BuildResult, c.BuildErr
}

func (c *Client) InspectContainer(ctx context.Context, name dockerapi.ContainerName) (dockerapi.ContainerState, error) {
	c.record("InspectContainer", name)
	c.mu.Lock()
	defer c.mu.Unlock()
	if state, ok := c.Containers[name]; ok {
		return *state, nil
	}
	return dockerapi.ContainerState{}, fmt.Errorf("%w: container %s", domain.ErrNotFound, name)
}

func (c *Client) CreateContainer(ctx context.Context, req dockerapi.CreateRequest) (dockerapi.ContainerID, error) {
	c.record("CreateContainer", req)
	if c.CreateErrOnce != nil {
		err := c.CreateErrOnce
		c.CreateErrOnce = nil
		return "", err
	}
	if c.CreateErr != nil {
		return "", c.CreateErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.Containers[req.Name]; exists {
		return "", fmt.Errorf("%w: container %s already exists", domain.ErrConflict, req.Name)
	}
	c.nextID++
	id := dockerapi.ContainerID(fmt.Sprintf("ctr-%03d", c.nextID))
	c.Containers[req.Name] = &dockerapi.ContainerState{
		ID:     id,
		Name:   req.Name,
		Image:  req.Image,
		Labels: req.Labels,
	}
	if req.Network != "" {
		c.createNetworks[id] = req.Network
	}
	if c.CreateHook != nil {
		hook := c.CreateHook
		c.mu.Unlock()
		hook(req)
		c.mu.Lock()
	}
	return id, nil
}

func (c *Client) findByID(id dockerapi.ContainerID) (*dockerapi.ContainerState, error) {
	for _, state := range c.Containers {
		if state.ID == id {
			return state, nil
		}
	}
	return nil, fmt.Errorf("%w: container id %s", domain.ErrNotFound, id)
}

func (c *Client) StartContainer(ctx context.Context, id dockerapi.ContainerID) error {
	c.record("StartContainer", id)
	if c.StartErr != nil {
		return c.StartErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.findByID(id)
	if err != nil {
		return err
	}
	if c.StartHookErr != nil {
		if err := c.StartHookErr(*state); err != nil {
			return err
		}
	}
	state.Running = !c.StartExits[id]
	if net := c.createNetworks[id]; net != "" && state.Running {
		ip := c.MintedIPs[id]
		if ip == "" {
			c.nextIP++
			ip = fmt.Sprintf("10.90.0.%d", c.nextIP)
			c.MintedIPs[id] = ip
		}
		state.Networks = map[string]string{net: ip}
	}
	return nil
}

func (c *Client) StopContainer(ctx context.Context, id dockerapi.ContainerID, timeout time.Duration) error {
	c.record("StopContainer", id)
	if c.StopErr != nil {
		return c.StopErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.findByID(id)
	if err != nil {
		return err
	}
	if c.StopHookErr != nil {
		if err := c.StopHookErr(*state); err != nil {
			return err
		}
	}
	state.Running = false
	state.Networks = nil
	return nil
}

func (c *Client) RemoveContainer(ctx context.Context, id dockerapi.ContainerID, opts dockerapi.RemoveOptions) error {
	c.record("RemoveContainer", id)
	if c.RemoveErr != nil {
		return c.RemoveErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, state := range c.Containers {
		if state.ID == id {
			if c.RemoveHookErr != nil {
				if err := c.RemoveHookErr(*state); err != nil {
					return err
				}
			}
			delete(c.Containers, name)
			return nil
		}
	}
	return fmt.Errorf("%w: container id %s", domain.ErrNotFound, id)
}

func (c *Client) RenameContainer(ctx context.Context, id dockerapi.ContainerID, name dockerapi.ContainerName) error {
	c.record("RenameContainer", struct {
		ID   dockerapi.ContainerID
		Name dockerapi.ContainerName
	}{id, name})
	if c.RenameErr != nil {
		return c.RenameErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.findByID(id)
	if err != nil {
		return err
	}
	if c.RenameHookErr != nil {
		if err := c.RenameHookErr(*state, name); err != nil {
			return err
		}
	}
	if state.Name == name {
		return nil
	}
	if _, exists := c.Containers[name]; exists {
		return fmt.Errorf("%w: container %s already exists", domain.ErrConflict, name)
	}
	delete(c.Containers, state.Name)
	state.Name = name
	c.Containers[name] = state
	return nil
}

func (c *Client) WaitContainer(ctx context.Context, id dockerapi.ContainerID) (int, error) {
	c.record("WaitContainer", id)
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.findByID(id)
	if err != nil {
		return 0, err
	}
	state.Running = false
	return c.WaitCodes[id], nil
}

func (c *Client) ListProjectContainers(ctx context.Context, project domain.ProjectID) ([]dockerapi.ContainerState, error) {
	c.record("ListProjectContainers", project)
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []dockerapi.ContainerState
	for _, state := range c.Containers {
		if state.Labels["dev.vibe.project"] == string(project) {
			out = append(out, *state)
		}
	}
	return out, nil
}

func (c *Client) ListManagedContainers(ctx context.Context) ([]dockerapi.ContainerState, error) {
	c.record("ListManagedContainers", nil)
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []dockerapi.ContainerState
	for _, state := range c.Containers {
		if state.Labels["dev.vibe.managed"] == "true" {
			out = append(out, *state)
		}
	}
	return out, nil
}

func (c *Client) Exec(ctx context.Context, req dockerapi.ExecRequest) (dockerapi.ExecResult, error) {
	c.record("Exec", req)
	if c.ExecErr != nil {
		return dockerapi.ExecResult{}, c.ExecErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if out, ok := c.ExecOutputs[ExecKey(req.Argv)]; ok && req.Streams.Out != nil {
		_, _ = req.Streams.Out.Write([]byte(out))
	}
	if res, ok := c.ExecResults[ExecKey(req.Argv)]; ok {
		return res, nil
	}
	return dockerapi.ExecResult{}, nil
}

// ExecKey builds the scripting key for ExecResults.
func ExecKey(argv []string) string {
	key := ""
	for i, a := range argv {
		if i > 0 {
			key += "\x00"
		}
		key += a
	}
	return key
}

func (c *Client) Attach(ctx context.Context, req dockerapi.AttachRequest) error {
	c.record("Attach", req)
	return nil
}

func (c *Client) Logs(ctx context.Context, req dockerapi.LogsRequest) error {
	c.record("Logs", req)
	if c.LogsErr != nil {
		return c.LogsErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.Containers[req.Container]; !ok {
		return fmt.Errorf("%w: container %s", domain.ErrNotFound, req.Container)
	}
	if out, ok := c.LogsOutputs[req.Container]; ok && req.Streams.Out != nil {
		_, _ = req.Streams.Out.Write([]byte(out))
	}
	return nil
}

func (c *Client) EnsureVolume(ctx context.Context, spec dockerapi.VolumeSpec) error {
	c.record("EnsureVolume", spec)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Volumes[spec.Name] = spec
	return nil
}

func (c *Client) RemoveVolume(ctx context.Context, name dockerapi.VolumeName) error {
	c.record("RemoveVolume", name)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.Volumes, string(name))
	return nil
}

func (c *Client) EnsureNetwork(ctx context.Context, spec dockerapi.NetworkSpec) error {
	c.record("EnsureNetwork", spec)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Networks[spec.Name] = spec
	return nil
}

func (c *Client) RemoveNetwork(ctx context.Context, name dockerapi.NetworkName) error {
	c.record("RemoveNetwork", name)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.Networks, string(name))
	return nil
}
