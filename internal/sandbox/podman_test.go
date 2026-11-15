package sandbox

import "testing"

func TestBuildCmdUsesContainerRelativePOSIXPath(t *testing.T) {
	engine := &Engine{}
	command := engine.buildCmd("agent.yaml", "report status")

	want := []string{"run", "--exec", "--last", "/workspace/agent.yaml", "report status"}
	if len(command) != len(want) {
		t.Fatalf("buildCmd() = %#v, want %#v", command, want)
	}
	for i := range want {
		if command[i] != want[i] {
			t.Fatalf("buildCmd()[%d] = %q, want %q; full command %#v", i, command[i], want[i], command)
		}
	}
}
