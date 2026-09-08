package bot

import (
	"iter"
	"math"
	"slices"
	"time"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/block/model"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/fzipp/astar"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/go-gl/mathgl/mgl64"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type Finder[Node cube.Pos] struct {
	w           *World
	c           *Client
	allowFlight bool
}

var blockList = []string{"minecraft:air", "minecraft:grass", "minecraft:seagrass", "minecraft:tallgrass"}

func WalkableBlock(b world.Block) bool {
	bb, _ := b.EncodeBlock()
	if slices.Contains(blockList, bb) {
		return true
	}
	return false
}

func (f Finder[_]) AllowStanding(n cube.Pos) bool {
	b := f.w.Block(n)
	b2 := f.w.Block(n.Side(cube.FaceUp))
	b3 := f.w.Block(n.Side(cube.FaceDown))

	if !f.allowFlight {
		if _, ok := b3.Model().(model.Empty); ok {
			return false
		}
	}

	if _, ok := b.Model().(model.Empty); ok {
		if _, ok := b2.Model().(model.Empty); ok {
			return true
		}
	}

	if WalkableBlock(b) && WalkableBlock(b2) {
		return true
	}

	return false
}

func (f Finder[Node]) Neighbours(no Node) (nodes iter.Seq[Node]) {
	n := cube.Pos(no)

	return func(yield func(Node) bool) {
		n.Neighbours(func(neighbour cube.Pos) {
			if f.AllowStanding(neighbour) {
				if !yield(Node(neighbour)) {
					return
				}
			}
		}, f.w.Range())
	}
}

func BlockPosFromVec3(position mgl32.Vec3) cube.Pos {
	position = vec3Floor(vec3Floor(position).Add(mgl32.Vec3{0.5, 0, 0.5}))

	pos := cube.Pos{int(position[0]), int(position[1]), int(position[2])}
	return pos
}

func (c *Client) FindPath(pos cube.Pos) astar.Path[cube.Pos] {
	f := Finder[cube.Pos]{c.World(), c, true}

	if !f.AllowStanding(pos) {
		return nil
	}
	position := c.Self.Position
	position = vec3Floor(vec3Floor(position).Add(mgl32.Vec3{0.5, 0, 0.5}))
	path := astar.FindPath[cube.Pos](f, cube.Pos{int(position[0]), int(position[1]), int(position[2])}, pos, DistanceTo, DistanceTo)
	return path
}

func DistanceTo(v cube.Pos, vec3d cube.Pos) float64 {
	xDiff, yDiff, zDiff := v.X()-vec3d.X(), v.Y()-vec3d.Y(), v.Z()-vec3d.Z()
	return math.Sqrt(float64(xDiff*xDiff + yDiff*yDiff + zDiff*zDiff))
}
func DistanceToVec3(v mgl32.Vec3, vec3d mgl32.Vec3) float64 {
	xDiff, yDiff, zDiff := v.X()-vec3d.X(), v.Y()-vec3d.Y(), v.Z()-vec3d.Z()
	return math.Sqrt(float64(xDiff*xDiff + yDiff*yDiff + zDiff*zDiff))
}

func (c *Client) FlyTo(position mgl32.Vec3) {
	defer c.flyLock.Unlock()
	c.flyLock.Lock()
	c.internalFlyTo(position)
}
// walkTickInterval matches the server's tick rate (20 ticks/second) so that
// position updates sent via PlayerAuthInput look like normal, physically
// plausible movement instead of teleports.
const walkTickInterval = 50 * time.Millisecond

// maxWalkStepPerTick caps how far the position can move in a single tick,
// approximating vanilla walking speed (~4.3 blocks/second). Servers that
// perform server-authoritative movement validation (common on anti-cheat
// setups such as the one this library targets) will reject or silently
// correct any PlayerAuthInput that reports a larger jump than this, which
// previously caused WalkTo to have no effect at all: the bot's position was
// snapped back by the server on every MovePlayer correction.
const maxWalkStepPerTick = 0.2158

func (c *Client) WalkTo(position mgl32.Vec3) {
	pos := cube.Pos{int(position[0]), int(position[1]), int(position[2])}
	paths := c.FindPath(pos)
	for _, path := range paths {
		target := mgl32.Vec3{float32(path[0]) + 0.5, float32(path[1]) + 1.62, float32(path[2]) + 0.5}
		c.stepTowards(target)
	}
	c.stepTowards(mgl32.Vec3{position.X(), position.Y() + 1.62, position.Z()})
}

// stepTowards moves c.Self.Position towards target in increments no larger
// than maxWalkStepPerTick, sending a PlayerAuthInput each tick, so the
// server's movement validation sees a realistic walking speed rather than a
// single large jump that gets rejected/corrected.
func (c *Client) stepTowards(target mgl32.Vec3) {
	for {
		next, arrived := nextWalkStep(c.Self.Position, target, maxWalkStepPerTick)
		c.Self.Position = next
		c.SendCurrentPosition()
		if arrived {
			return
		}
		time.Sleep(walkTickInterval)
	}
}

// nextWalkStep computes the position after moving from current towards
// target by at most maxStep. If the remaining distance is within maxStep,
// it returns target exactly and reports arrived as true. This is a pure
// function so the walking speed cap can be unit tested without a live
// network connection.
func nextWalkStep(current, target mgl32.Vec3, maxStep float32) (next mgl32.Vec3, arrived bool) {
	delta := target.Sub(current)
	dist := delta.Len()
	if dist <= maxStep {
		return target, true
	}
	return current.Add(delta.Mul(maxStep / dist)), false
}
func FromBlockPos(v mgl32.Vec3) mgl32.Vec3 {
	newX := math.Floor(float64(v.X()))
	newY := math.Floor(float64(v.Y()))
	newZ := math.Floor(float64(v.Z()))
	if v.X() != 0 {
		//newX = newX + 0.5
		if v.X() > 0 {
			newX = newX + 0.5
		} else {
			newX = newX - 0.5
		}
	}
	//newY = newY
	if v.Z() != 0 {
		//newZ = newZ + 0.5
		if v.Z() > 0 {
			newZ = newZ + 0.5
		} else {
			newZ = newZ - 0.5
		}
	}

	return mgl32.Vec3{float32(newX), float32(newY), float32(newZ)}
}

var eyeY = mgl32.Vec3{0, 1.62, 0}

func (c *Client) SendCurrentPosition() {
	c.Conn.WritePacket(&packet.PlayerAuthInput{
		InputData: protocol.NewBitset(packet.PlayerAuthInputBitsetSize),
		Position:  c.Self.Position,
		Pitch:     c.Self.Pitch,
		Yaw:       c.Self.Yaw,
		HeadYaw:   c.Self.HeadYaw,
		Tick:      c.inputTick.Add(1),
	})
}

func (c *Client) SendInputData(flags ...int) {
	inputData := protocol.NewBitset(packet.PlayerAuthInputBitsetSize)
	for _, flag := range flags {
		inputData.Set(flag)
	}

	c.Conn.WritePacket(&packet.PlayerAuthInput{
		InputData: inputData,
		Position:  c.Self.Position,
		Pitch:     c.Self.Pitch,
		Yaw:       c.Self.Yaw,
		HeadYaw:   c.Self.HeadYaw,
		Tick:      c.inputTick.Add(1),
	})
}

func (c *Client) SendCustomPosition(position mgl32.Vec3) {
	c.Conn.WritePacket(&packet.PlayerAuthInput{
		InputData: protocol.NewBitset(packet.PlayerAuthInputBitsetSize),
		Position:  position,
		Pitch:     c.Self.Pitch,
		Yaw:       c.Self.Yaw,
		HeadYaw:   c.Self.HeadYaw,
		Tick:      c.inputTick.Add(1),
	})
}

func (c *Client) internalFlyTo(position mgl32.Vec3) {
	position = position.Add(eyeY)
	vector := position.Sub(c.Self.Position)
	magnitude := vector.Len()
	for magnitude > 9 {
		mV := vector.Mul(9 / magnitude)
		//mV = mV.Mul(10)
		c.Self.Position = c.Self.Position.Add(mV)

		c.SendCurrentPosition()
		time.Sleep(50 * time.Millisecond)
		//c.Player.SetPosition(c.Player.Position.X, c.Player.Position.Y, c.Player.Position.Z)
		vector = position.Sub(c.Self.Position)
		magnitude = vector.Len()
	}
	time.Sleep(50 * time.Millisecond)
	c.Self.Position = position
	c.SendCurrentPosition()
	//c.Player.SetPosition(position.X, position.Y, position.Z)
	//c.Player.SendCustomPosition(c, position)
}
func vec3Floor(v mgl32.Vec3) mgl32.Vec3 {
	vec3 := vec32To64(v)
	return vec64To32(mgl64.Vec3{math.Floor(vec3[0]), math.Floor(vec3[1]), math.Floor(vec3[2])})
}
