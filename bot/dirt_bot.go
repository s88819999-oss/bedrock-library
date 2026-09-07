package bot

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// DirtBotConfig controls the commands used by a dirt mining bot.
type DirtBotConfig struct {
	StorageCommand string
	ReturnCommand  string
	RandomCommand  string
	ScanRadius     int
	ScanHeight     int
	ScanInterval   time.Duration
	CommandDelay   time.Duration
}

// RunDirtBot mines loaded dirt blocks until the context is cancelled. When the
// inventory is full, it visits storage, drops dirt, returns, and randomises its
// mining area.
func (c *Client) RunDirtBot(ctx context.Context, config DirtBotConfig) {
	if config.ScanRadius <= 0 {
		config.ScanRadius = 32
	}
	if config.ScanHeight <= 0 {
		config.ScanHeight = 10
	}
	if config.ScanInterval <= 0 {
		config.ScanInterval = 500 * time.Millisecond
	}
	if config.CommandDelay <= 0 {
		config.CommandDelay = 2 * time.Second
	}
	if config.ReturnCommand != "" {
		if err := c.SendCommand(config.ReturnCommand); err != nil {
			c.Logger.Warn(err)
		}
		waitContext(ctx, config.CommandDelay)
	}
	if config.RandomCommand != "" {
		if err := c.SendCommand(config.RandomCommand); err != nil {
			c.Logger.Warn(err)
		}
		waitContext(ctx, config.CommandDelay)
	}
	noDirtScans := 0
	lastScanLog := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if c.Screen == nil || c.World() == nil || c.Self == nil {
			time.Sleep(config.ScanInterval)
			continue
		}
		if inventoryFull(c) {
			if err := c.emptyDirtAndResume(ctx, config); err != nil {
				c.Logger.Warn(err)
			}
			continue
		}

		shovel := findShovel(c)
		if shovel == -1 {
			c.Logger.Warn("dirt bot is waiting for a shovel in hotbar slots 0-8")
			waitContext(ctx, 5*time.Second)
			continue
		}
		c.Screen.SetCarriedItem(shovel)

		pos, stand, ok := nearestDirt(c, config.ScanRadius, config.ScanHeight)
		if !ok {
			noDirtScans++
			if time.Since(lastScanLog) >= 10*time.Second {
				base := BlockPosFromVec3(c.Self.Position)
				chunkLoaded := c.World().Chunk(world.ChunkPos{int32(base.X() >> 4), int32(base.Z() >> 4)}) != nil
				center, _ := c.World().Block(base).EncodeBlock()
				// Sample some blocks to see what's actually loaded
				sampleBlocks := []cube.Pos{
					base,
					base.Side(cube.FaceUp),
					base.Side(cube.FaceDown),
					base.Add(cube.Pos{1, 0, 0}),
					base.Add(cube.Pos{-1, 0, 0}),
					base.Add(cube.Pos{0, 0, 1}),
					base.Add(cube.Pos{0, 0, -1}),
				}
				blocks := []string{}
				for _, sb := range sampleBlocks {
					bn, _ := c.World().Block(sb).EncodeBlock()
					blocks = append(blocks, bn)
				}
				c.Logger.Infof("dirt bot found no target near %v (chunk_loaded=%t, center=%s, samples=%v)", base, chunkLoaded, center, blocks)
				lastScanLog = time.Now()
			}
			if noDirtScans >= 3 && config.RandomCommand != "" {
				c.Logger.Infof("no dirt found after %d scans; running %s", noDirtScans, config.RandomCommand)
				if err := c.SendCommand(config.RandomCommand); err != nil {
					c.Logger.Warn(err)
				}
				noDirtScans = 0
				waitContext(ctx, config.CommandDelay)
				continue
			}
			waitContext(ctx, 5*time.Second)
			continue
		}

		noDirtScans = 0
		c.Logger.Infof("mining dirt at %v from %v", pos, stand)
		c.WalkTo(mgl32.Vec3{float32(stand.X()) + 0.5, float32(stand.Y()), float32(stand.Z()) + 0.5})
		c.BreakBlock(pos)
	}
}

func nearestDirt(c *Client, radius, height int) (cube.Pos, cube.Pos, bool) {
	base := BlockPosFromVec3(c.Self.Position)
	bestDistance := math.MaxInt
	var best, bestStand cube.Pos
	found := false
	for x := base.X() - radius; x <= base.X()+radius; x++ {
		for y := base.Y() - height; y <= base.Y()+height; y++ {
			for z := base.Z() - radius; z <= base.Z()+radius; z++ {
				pos := cube.Pos{x, y, z}
				name, _ := c.World().Block(pos).EncodeBlock()
				if !isDigTarget(name) {
					continue
				}
				c.Logger.Debugf("found dig target: %s at %v", name, pos)
				stand, ok := standingBeside(c, pos)
				if !ok {
					stand = pos.Side(cube.FaceUp)
				}
				distance := (x-base.X())*(x-base.X()) + (y-base.Y())*(y-base.Y()) + (z-base.Z())*(z-base.Z())
				if !found || distance < bestDistance {
					best, bestStand, bestDistance, found = pos, stand, distance, true
				}
			}
		}
	}
	return best, bestStand, found
}

func isDigTarget(name string) bool {
	return strings.Contains(name, "dirt") || strings.Contains(name, "grass")
}

func standingBeside(c *Client, target cube.Pos) (cube.Pos, bool) {
	finder := Finder[cube.Pos]{w: c.World(), c: c, allowFlight: false}
	for _, stand := range []cube.Pos{
		{target.X() + 1, target.Y(), target.Z()},
		{target.X() - 1, target.Y(), target.Z()},
		{target.X(), target.Y(), target.Z() + 1},
		{target.X(), target.Y(), target.Z() - 1},
		{target.X(), target.Y() + 1, target.Z()},
	} {
		if finder.AllowStanding(stand) {
			return stand, true
		}
	}
	return cube.Pos{}, false
}

func findShovel(c *Client) int {
	for slot, stack := range c.Screen.Inv.Slots()[:9] {
		if stack.Empty() {
			continue
		}
		name, _ := stack.Item().EncodeItem()
		if strings.HasSuffix(name, "_shovel") {
			return slot
		}
	}
	return -1
}

func inventoryFull(c *Client) bool {
	for _, stack := range c.Screen.Inv.Slots() {
		if stack.Empty() {
			return false
		}
	}
	return true
}

func (c *Client) emptyDirtAndResume(ctx context.Context, config DirtBotConfig) error {
	if config.StorageCommand != "" {
		if err := c.SendCommand(config.StorageCommand); err != nil {
			return err
		}
		if !waitContext(ctx, config.CommandDelay) {
			return ctx.Err()
		}
	}

	for slot, stack := range c.Screen.Inv.Slots() {
		name, _ := stack.Item().EncodeItem()
		if stack.Empty() || !isDigTarget(name) {
			continue
		}
		action := &protocol.DropStackRequestAction{
			Source: protocol.StackRequestSlotInfo{
				Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
				Slot:           byte(slot),
				StackNetworkID: -1,
			},
			Count: byte(stack.Count()),
		}
		if err := c.Screen.SendContainerClick(c.Screen.PackingRequests(action)); err != nil {
			return fmt.Errorf("drop dirt from slot %d: %w", slot, err)
		}
		if !waitContext(ctx, 150*time.Millisecond) {
			return ctx.Err()
		}
	}

	for _, command := range []string{config.ReturnCommand, config.RandomCommand} {
		if command == "" {
			continue
		}
		if err := c.SendCommand(command); err != nil {
			return err
		}
		if !waitContext(ctx, config.CommandDelay) {
			return ctx.Err()
		}
	}
	return nil
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
