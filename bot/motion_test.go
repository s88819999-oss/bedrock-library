package bot

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestNextWalkStepArrivesWhenWithinRange(t *testing.T) {
	current := mgl32.Vec3{0, 0, 0}
	target := mgl32.Vec3{0.1, 0, 0}

	next, arrived := nextWalkStep(current, target, maxWalkStepPerTick)
	if !arrived {
		t.Fatalf("expected arrived=true when distance %v is within maxStep %v", target.Sub(current).Len(), maxWalkStepPerTick)
	}
	if next != target {
		t.Fatalf("expected next to equal target exactly on arrival, got %v want %v", next, target)
	}
}

func TestNextWalkStepCapsDistancePerTick(t *testing.T) {
	current := mgl32.Vec3{0, 0, 0}
	target := mgl32.Vec3{10, 0, 0} // far away, requires many ticks

	next, arrived := nextWalkStep(current, target, maxWalkStepPerTick)
	if arrived {
		t.Fatalf("did not expect arrived=true for a distant target")
	}

	dist := next.Sub(current).Len()
	if dist > maxWalkStepPerTick+1e-4 {
		t.Fatalf("step distance %v exceeded maxWalkStepPerTick %v; a server with movement validation would reject this as teleporting", dist, maxWalkStepPerTick)
	}

	// The step should move directly towards the target (same direction).
	if next.X() <= current.X() || next.Y() != current.Y() || next.Z() != current.Z() {
		t.Fatalf("expected step to move only along X towards target, got %v", next)
	}
}

func TestNextWalkStepConvergesToTarget(t *testing.T) {
	current := mgl32.Vec3{-5, 12, 3}
	target := mgl32.Vec3{7, 12, -9}

	const maxTicks = 1000
	ticks := 0
	for {
		var arrived bool
		current, arrived = nextWalkStep(current, target, maxWalkStepPerTick)
		ticks++
		if arrived {
			break
		}
		if ticks > maxTicks {
			t.Fatalf("did not converge to target within %d ticks; current=%v target=%v", maxTicks, current, target)
		}
	}
	if current != target {
		t.Fatalf("expected final position to equal target, got %v want %v", current, target)
	}
}

func TestNextWalkStepNoOpWhenAlreadyAtTarget(t *testing.T) {
	pos := mgl32.Vec3{3, 4, 5}
	next, arrived := nextWalkStep(pos, pos, maxWalkStepPerTick)
	if !arrived {
		t.Fatalf("expected arrived=true when already at target")
	}
	if next != pos {
		t.Fatalf("expected next to equal current position, got %v", next)
	}
}
