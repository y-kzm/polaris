package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	gorav1 "controller/backend/api/gora/v1"
	"controller/backend/internal/store"
)

// opLog records the agent calls of all routers in the order they happened.
type opLog struct {
	mu  sync.Mutex
	ops []string
}

func (l *opLog) add(op string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ops = append(l.ops, op)
}

func (l *opLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ops...)
}

// fakeAgent is an in-memory go-ra agent.
type fakeAgent struct {
	name    string
	log     *opLog
	configs map[int32]*gorav1.InterfaceConfig
	failAdd bool
}

func newFakeAgent(name string, log *opLog) *fakeAgent {
	return &fakeAgent{name: name, log: log, configs: map[int32]*gorav1.InterfaceConfig{0: baseline}}
}

func (a *fakeAgent) ListInterfaces(context.Context) ([]*gorav1.InterfaceConfig, error) {
	var out []*gorav1.InterfaceConfig
	for _, c := range a.configs {
		out = append(out, c)
	}
	return out, nil
}

func (a *fakeAgent) AddInterface(_ context.Context, cfg *gorav1.InterfaceConfig) error {
	if a.failAdd {
		return errors.New("boom")
	}
	a.log.add(a.name + " make add")
	a.configs[cfg.Id] = cfg
	return nil
}

func (a *fakeAgent) UpdateInterface(_ context.Context, cfg *gorav1.InterfaceConfig) error {
	a.log.add(a.name + " make update")
	a.configs[cfg.Id] = cfg
	return nil
}

func (a *fakeAgent) DeleteInterface(_ context.Context, id int32, skipGoodbye bool) error {
	if !skipGoodbye {
		return errors.New("the engine must delete without a goodbye RA")
	}
	a.log.add(a.name + " break delete")
	delete(a.configs, id)
	return nil
}

func (a *fakeAgent) SendRA(_ context.Context, cfg *gorav1.InterfaceConfig, withdrawn []string) error {
	phase := "make advertise"
	if len(withdrawn) > 0 || cfg.Id == 0 {
		phase = "break withdraw"
	}
	a.log.add(a.name + " " + phase + " -> " + strings.Join(cfg.Clients, ","))
	return nil
}

func (a *fakeAgent) Close() {}

type fakeNet struct {
	log    *opLog
	agents map[string]*fakeAgent
	down   map[string]bool
}

func newFakeNet() *fakeNet {
	log := &opLog{}
	return &fakeNet{log: log, down: map[string]bool{}, agents: map[string]*fakeAgent{
		"a": newFakeAgent("a", log),
		"b": newFakeAgent("b", log),
	}}
}

func (n *fakeNet) dial(r *store.Router) (Agent, error) {
	if n.down[r.ID] {
		return nil, errors.New("unreachable")
	}
	return n.agents[r.ID], nil
}

func (n *fakeNet) managed(id string) int {
	count := 0
	for _, c := range n.agents[id].configs {
		if isManaged(c) {
			count++
		}
	}
	return count
}

var (
	defaultViaA = []store.Rule{rule(1, routerA.Address, "::/0")}
	defaultViaB = []store.Rule{rule(1, routerB.Address, "::/0")}
	oneGroup    = []store.Group{group("G", []int{1}, "fe80::1")}
)

// A host that moves from one router to another must be told about the new
// router before the old one is withdrawn.
func TestApplyMakesBeforeBreaking(t *testing.T) {
	net := newFakeNet()
	if res := Apply(context.Background(), net.dial, defaultViaA, oneGroup, testRouters, false); !res.Success {
		t.Fatalf("first apply failed: %+v", res)
	}
	before := len(net.log.snapshot())

	res := Apply(context.Background(), net.dial, defaultViaB, oneGroup, testRouters, false)
	if !res.Success {
		t.Fatalf("second apply failed: %+v", res)
	}

	ops := net.log.snapshot()[before:]
	lastMake, firstBreak := -1, len(ops)
	for i, op := range ops {
		if strings.Contains(op, " make ") {
			lastMake = i
		} else if i < firstBreak {
			firstBreak = i
		}
	}
	if lastMake == -1 || firstBreak == len(ops) || lastMake > firstBreak {
		t.Fatalf("make must precede break: %v", ops)
	}
	want := []string{"b make add", "b make advertise -> fe80::1", "a break delete", "a break withdraw -> fe80::1"}
	if strings.Join(ops, "; ") != strings.Join(want, "; ") {
		t.Errorf("ops = %v\nwant  %v", ops, want)
	}
	if net.managed("a") != 0 || net.managed("b") != 1 {
		t.Errorf("managed configs: a=%d b=%d; want 0, 1", net.managed("a"), net.managed("b"))
	}
	if r := res.Results[0]; r.Deleted != 1 || r.Withdrawn != 1 {
		t.Errorf("router a result = %+v", r)
	}
}

// If the new router cannot take the policy, the old router keeps it.
func TestApplySkipsBreakWhenMakeFails(t *testing.T) {
	net := newFakeNet()
	Apply(context.Background(), net.dial, defaultViaA, oneGroup, testRouters, false)
	before := len(net.log.snapshot())
	net.agents["b"].failAdd = true

	res := Apply(context.Background(), net.dial, defaultViaB, oneGroup, testRouters, false)

	if res.Success {
		t.Fatal("apply must report the failure")
	}
	if ops := net.log.snapshot()[before:]; len(ops) != 0 {
		t.Errorf("nothing must be withdrawn, got %v", ops)
	}
	if net.managed("a") != 1 {
		t.Errorf("router a lost its policy configuration")
	}
	for _, r := range res.Results {
		if r.Success {
			t.Errorf("%s reported success: %+v", r.Router, r)
		}
	}
}

// With a router unreachable, the new policy is installed where possible but
// nothing is withdrawn: a host whose new router is the unreachable one keeps
// its old router.
func TestApplyWithUnreachableRouterKeepsOldPolicy(t *testing.T) {
	net := newFakeNet()
	Apply(context.Background(), net.dial, defaultViaA, oneGroup, testRouters, false)
	before := len(net.log.snapshot())
	net.down["b"] = true

	res := Apply(context.Background(), net.dial, defaultViaB, oneGroup, testRouters, false)

	if res.Success || res.Results[0].Success || res.Results[1].Success {
		t.Fatalf("results = %+v", res.Results)
	}
	if ops := net.log.snapshot()[before:]; len(ops) != 0 {
		t.Errorf("nothing must be withdrawn, got %v", ops)
	}
	if net.managed("a") != 1 || res.Results[0].Withdrawn != 0 {
		t.Errorf("router a lost its policy: managed=%d result=%+v", net.managed("a"), res.Results[0])
	}

	// Once the router is back, applying again completes the transition.
	net.down["b"] = false
	if res := Apply(context.Background(), net.dial, defaultViaB, oneGroup, testRouters, false); !res.Success {
		t.Fatalf("retry failed: %+v", res)
	}
	if net.managed("a") != 0 || net.managed("b") != 1 {
		t.Errorf("after retry: a=%d b=%d; want 0, 1", net.managed("a"), net.managed("b"))
	}
}

func TestApplyDryRunChangesNothing(t *testing.T) {
	net := newFakeNet()

	res := Apply(context.Background(), net.dial, defaultViaB, oneGroup, testRouters, true)

	if !res.Success || !res.DryRun || res.Results[1].Added != 1 {
		t.Fatalf("result = %+v", res)
	}
	if ops := net.log.snapshot(); len(ops) != 0 {
		t.Errorf("dry run changed the routers: %v", ops)
	}
}

// Deleting every rule and applying again must clean the routers up.
func TestApplyRemovesStalePolicy(t *testing.T) {
	net := newFakeNet()
	Apply(context.Background(), net.dial, defaultViaA, oneGroup, testRouters, false)

	res := Apply(context.Background(), net.dial, nil, nil, testRouters, false)

	if !res.Success || net.managed("a") != 0 {
		t.Fatalf("stale configuration left: %+v", res)
	}
	if _, ok := net.agents["a"].configs[0]; !ok {
		t.Error("the baseline configuration was removed")
	}
}
