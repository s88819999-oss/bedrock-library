package bot

import (
	"reflect"
	"testing"

	"github.com/df-mc/dragonfly/server/block"
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/item"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func TestFilterWaterTargets(t *testing.T) {
	targets := []cube.Pos{
		{0, 64, 0},
		{0, 64, 1},
		{1, 64, 0},
		{1, 64, 1},
	}

	got := filterWaterTargets(targets, func(pos cube.Pos) string {
		switch pos {
		case cube.Pos{0, 64, 0}:
			return "minecraft:air"
		case cube.Pos{0, 64, 1}:
			return "minecraft:stone"
		case cube.Pos{1, 64, 0}:
			return "minecraft:water"
		case cube.Pos{1, 64, 1}:
			return "minecraft:air"
		default:
			return "minecraft:air"
		}
	})

	want := []cube.Pos{{0, 64, 0}, {1, 64, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterWaterTargets() = %v, want %v", got, want)
	}
}

func TestIsWaterBucketName(t *testing.T) {
	if !isWaterBucketName("minecraft:water_bucket") {
		t.Fatal("expected minecraft:water_bucket to be treated as water bucket")
	}
	if isWaterBucketName("minecraft:bucket") {
		t.Fatal("bucket should not be treated as water bucket")
	}
}

func TestWaterFillCompleted(t *testing.T) {
	positions := []cube.Pos{{0, 64, 0}, {0, 64, 1}}
	if waterFillCompleted(func(pos cube.Pos) string {
		switch pos {
		case cube.Pos{0, 64, 0}:
			return "minecraft:water"
		case cube.Pos{0, 64, 1}:
			return "minecraft:water"
		default:
			return "minecraft:air"
		}
	}, positions) != true {
		t.Fatal("expected all water targets to count as complete")
	}

	if waterFillCompleted(func(pos cube.Pos) string {
		switch pos {
		case cube.Pos{0, 64, 0}:
			return "minecraft:water"
		case cube.Pos{0, 64, 1}:
			return "minecraft:air"
		default:
			return "minecraft:air"
		}
	}, positions) != false {
		t.Fatal("expected partially filled water targets to remain incomplete")
	}
}

func TestBuildUseItemTransactionUsesClickBlockAction(t *testing.T) {
	clickedBlock := cube.Pos{10, 63, 20}
	targetPos := cube.Pos{10, 64, 20}
	runtimeID := world.BlockRuntimeID(block.Stone{})
	transaction := buildUseItemTransaction(clickedBlock, targetPos, cube.FaceUp, 3, item.NewStack(item.Bucket{}, 1), mgl32.Vec3{10.5, 64.0, 20.5}, runtimeID)

	if transaction.ActionType != protocol.UseItemActionClickBlock {
		t.Fatalf("expected click-block action, got %d", transaction.ActionType)
	}
	if transaction.TriggerType != protocol.TriggerTypePlayerInput {
		t.Fatalf("expected player input trigger, got %d", transaction.TriggerType)
	}
	if transaction.BlockPosition != (protocol.BlockPos{10, 63, 20}) {
		t.Fatalf("expected clicked block position %v, got %v", protocol.BlockPos{10, 63, 20}, transaction.BlockPosition)
	}
	if transaction.BlockFace != int32(cube.FaceUp) {
		t.Fatalf("expected block face up, got %d", transaction.BlockFace)
	}
	if transaction.HotBarSlot != 3 {
		t.Fatalf("expected hotbar slot 3, got %d", transaction.HotBarSlot)
	}
	if transaction.BlockRuntimeID != runtimeID {
		t.Fatalf("expected block runtime ID %d, got %d", runtimeID, transaction.BlockRuntimeID)
	}
}
