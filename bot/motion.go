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

	if f.c != nil && f.c.Logger != nil {
		bName, _ := b.EncodeBlock()
		b2Name, _ := b2.EncodeBlock()
		b3Name, _ := b3.EncodeBlock()
		f.c.Logger.Infof("AllowStanding n=%v b=%s(%T) b2=%s(%T) b3=%s(%T)", n, bName, b.Model(), b2Name, b2.Model(), b3Name, b3.Model())
	}

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
	c.Logger.Infof("WalkTo target=%v pathLen=%d", position, len(paths))
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
	c.Logger.Infof("stepTowards start=%v target=%v", c.Self.Position, target)
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

// yawFromDelta computes the Bedrock yaw (in degrees; 0 = south/+Z, 90 =
// west/-X, matching the engine's rotate-clockwise-from-above convention) that
// corresponds to a horizontal movement delta (dx, dz).
func yawFromDelta(dx, dz float32) float32 {
	yaw := float32(math.Atan2(float64(-dx), float64(dz)) * 180 / math.Pi)
	if yaw < 0 {
		yaw += 360
	}
	return yaw
}

// sendAuthInput builds and sends a PlayerAuthInput packet. Earlier versions
// of this code only filled in Position/Pitch/Yaw/HeadYaw/Tick, leaving
// InputMode, PlayMode and InteractionModel at their zero value and
// MoveVector/Delta/AnalogueMoveVector/RawMoveVector at {0,0(,0)}. A real
// client never reports InputMode 0 (valid values start at 1) and always
// reports a MoveVector/Delta consistent with how far it actually moved that
// tick. Servers that authoritatively validate movement use exactly these
// fields to sanity-check (and otherwise silently reject or ignore) the
// reported Position, which is why WalkTo previously updated the bot's local
// Self.Position without the character ever visibly moving in-game.
func (c *Client) sendAuthInput(position mgl32.Vec3, inputData protocol.Bitset) {
	delta := position.Sub(c.lastAuthPosition)
	if !c.lastAuthValid {
		delta = mgl32.Vec3{}
	}

	moveVector := mgl32.Vec2{}
	if horizontal := (mgl32.Vec2{delta.X(), delta.Z()}); horizontal.Len() > 1e-4 {
		yaw := yawFromDelta(delta.X(), delta.Z())
		c.Self.Yaw = yaw
		c.Self.HeadYaw = yaw
		moveVector = mgl32.Vec2{0, 1}
	}

	c.lastAuthPosition = position
	c.lastAuthValid = true

	c.Conn.WritePacket(&packet.PlayerAuthInput{
		InputData:          inputData,
		Position:           position,
		Pitch:              c.Self.Pitch,
		Yaw:                c.Self.Yaw,
		HeadYaw:            c.Self.HeadYaw,
		InputMode:          packet.InputModeMouse,
		PlayMode:           packet.PlayModeNormal,
		InteractionModel:   packet.InteractionModelCrosshair,
		Tick:               c.inputTick.Add(1),
		Delta:              delta,
		MoveVector:         moveVector,
		AnalogueMoveVector: moveVector,
		RawMoveVector:      moveVector,
	})
}

func (c *Client) SendCurrentPosition() {
	c.sendAuthInput(c.Self.Position, protocol.NewBitset(packet.PlayerAuthInputBitsetSize))
}

func (c *Client) SendInputData(flags ...int) {
	inputData := protocol.NewBitset(packet.PlayerAuthInputBitsetSize)
	for _, flag := range flags {
		inputData.Set(flag)
	}
	c.sendAuthInput(c.Self.Position, inputData)
}

func (c *Client) SendCustomPosition(position mgl32.Vec3) {
	c.sendAuthInput(position, protocol.NewBitset(packet.PlayerAuthInputBitsetSize))
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
