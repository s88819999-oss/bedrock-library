package bot

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/go-gl/mathgl/mgl32"
)

// WaterFillBotConfig configures a water-filling bot for a staircase or tower interior.
type WaterFillBotConfig struct {
	Delay       time.Duration
	MaxAttempts int
}

// RunWaterFillBot fills the given positions one block at a time with water.
// Positions should be the air cells that need to become water. The bot picks a
// water bucket from the hotbar, walks to each valid target, and places the water.
func (c *Client) RunWaterFillBot(ctx context.Context, positions []cube.Pos, config WaterFillBotConfig) error {
	if config.Delay <= 0 {
		config.Delay = 200 * time.Millisecond
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 1
	}
	if c.Screen == nil || c.World() == nil || c.Self == nil {
		return fmt.Errorf("client not ready for water fill bot")
	}

	waterSlot, err := findWaterBucket(c)
	if err != nil {
		return err
	}
	c.Screen.SetCarriedItem(waterSlot)

	filtered := filterWaterTargets(positions, func(pos cube.Pos) string {
		if c.World() == nil {
			return ""
		}
		name, _ := c.World().Block(pos).EncodeBlock()
		return name
	})
	if len(filtered) == 0 {
		return nil
	}

	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		for _, pos := range filtered {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if !isWaterPlacementTarget(c, pos) {
				continue
			}

			if stand, ok := standingBeside(c, pos); ok {
				c.WalkTo(mgl32.Vec3{float32(stand.X()) + 0.5, float32(stand.Y()), float32(stand.Z()) + 0.5})
			}

			c.PlaceBlock(pos)
			if !waitContext(ctx, config.Delay) {
				return ctx.Err()
			}
		}
		if allWaterBlocks(c, filtered) {
			return nil
		}
	}

	return fmt.Errorf("water fill finished with %d unfinished cells", len(filtered)-countWaterBlocks(c, filtered))
}

// FillWaterBlocks is a convenience wrapper for a single pass.
func (c *Client) FillWaterBlocks(ctx context.Context, positions []cube.Pos) error {
	return c.RunWaterFillBot(ctx, positions, WaterFillBotConfig{Delay: 250 * time.Millisecond, MaxAttempts: 1})
}

// FishTowerFillConfig describes the stair-like fish-tower interior used for water filling.
// Row count is the total number of horizontal layers; the generated positions follow the
// same min/max X/Z boundary and keep a one-block empty gap pattern between fill cells.
type FishTowerFillConfig struct {
	MinX, MaxX int
	MinY, MaxY int
	MinZ, MaxZ int
	Rows       int
	GapEvery   int
}

// GenerateFishTowerFillPositions builds the fill-water coordinates for a fish tower.
// Use a small GapEvery (for example 2 or 3) to leave one block empty between filled cells
// while keeping the shape inside the given boundary.
func GenerateFishTowerFillPositions(cfg FishTowerFillConfig) []cube.Pos {
	if cfg.Rows <= 0 {
		cfg.Rows = 10
	}
	if cfg.GapEvery <= 0 {
		cfg.GapEvery = 2
	}
	if cfg.MinX > cfg.MaxX {
		cfg.MinX, cfg.MaxX = cfg.MaxX, cfg.MinX
	}
	if cfg.MinZ > cfg.MaxZ {
		cfg.MinZ, cfg.MaxZ = cfg.MaxZ, cfg.MinZ
	}
	if cfg.MinY > cfg.MaxY {
		cfg.MinY, cfg.MaxY = cfg.MaxY, cfg.MinY
	}

	positions := make([]cube.Pos, 0)
	for row := 0; row < cfg.Rows; row++ {
		y := cfg.MinY + (row*(cfg.MaxY-cfg.MinY+1))/max(1, cfg.Rows)
		for x := cfg.MinX; x <= cfg.MaxX; x++ {
			for z := cfg.MinZ; z <= cfg.MaxZ; z++ {
				if (x-cfg.MinX+z-cfg.MinZ+row)%cfg.GapEvery == 0 {
					continue
				}
				positions = append(positions, cube.Pos{x, y, z})
			}
		}
	}
	return positions
}

func (c *Client) AutoFillCobblestoneStairs(ctx context.Context, center cube.Pos, radius int, refillEvery int) error {
	if radius <= 0 {
		radius = 16
	}
	if refillEvery <= 0 {
		refillEvery = 3
	}

	targets := findCobblestoneStairWaterTargets(c, center, radius)
	if len(targets) == 0 {
		return fmt.Errorf("no cobblestone stair water targets detected near %v", center)
	}
	sort.Slice(targets, func(i, j int) bool {
		return distanceSquared(targets[i], center) < distanceSquared(targets[j], center)
	})
	if len(targets) > 10 {
		targets = targets[:10]
	}
	c.Logger.Infof("偵測到 %d 個階梯附近的填水目標", len(targets))

	completed := 0
	for _, pos := range targets {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := c.ensureWaterBucket(ctx, 64); err != nil {
			return err
		}

		waterSlot, err := findWaterBucket(c)
		if err != nil {
			return err
		}
		c.Screen.SetCarriedItem(waterSlot)

		if !isWaterPlacementTarget(c, pos) {
			continue
		}
		if stand, ok := standingBeside(c, pos); ok {
			c.WalkTo(mgl32.Vec3{float32(stand.X()) + 0.5, float32(stand.Y()), float32(stand.Z()) + 0.5})
		}
		c.PlaceBlock(pos)
		if !waitContext(ctx, 250*time.Millisecond) {
			return ctx.Err()
		}
		if !waitForWaterPlacementConfirmation(ctx, c, pos, waterSlot, 2*time.Second) {
			c.Logger.Warnf("放水未確認成功，位置=%v", pos)
			continue
		}
		completed++
	}

	if completed == 0 {
		return fmt.Errorf("no water placement was confirmed")
	}
	if !waterFillCompleted(func(pos cube.Pos) string {
		if c.World() == nil {
			return ""
		}
		name, _ := c.World().Block(pos).EncodeBlock()
		return name
	}, targets) {
		if completed == len(targets) {
			c.Logger.Warnf("世界快取未能確認所有水方塊，但背包狀態已確認完成 %d/%d 次放置", completed, len(targets))
			return nil
		}
		return fmt.Errorf("water placement incomplete: %d/%d targets confirmed", completed, len(targets))
	}
	return nil
}

func distanceSquared(a, b cube.Pos) int {
	x, y, z := a.X()-b.X(), a.Y()-b.Y(), a.Z()-b.Z()
	return x*x + y*y + z*z
}

func waitForWater(ctx context.Context, c *Client, pos cube.Pos, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		name := ""
		if c.World() != nil {
			name, _ = c.World().Block(pos).EncodeBlock()
		}
		if strings.Contains(name, "water") {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

func waitForWaterPlacementConfirmation(ctx context.Context, c *Client, pos cube.Pos, waterSlot int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		if nearbyWaterFound(c, pos, 0) {
			return true
		}
		if slotIsNoLongerWaterBucket(c, waterSlot) {
			// The bucket was consumed by the server, but the world cache at pos
			// hasn't reflected a water block yet (block update packets can lag
			// slightly behind the inventory update). Give it a short grace
			// period and check a small neighbourhood before trusting the
			// bucket-depletion alone, since bucket usage does not guarantee
			// the block placement itself succeeded (e.g. the target cell may
			// have turned out to already be solid).
			return waitForNearbyWater(ctx, c, pos, 1, time.Second)
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

// nearbyWaterFound reports whether any block within radius blocks of pos
// (inclusive) currently has a name containing "water".
func nearbyWaterFound(c *Client, pos cube.Pos, radius int) bool {
	if c.World() == nil {
		return false
	}
	for dx := -radius; dx <= radius; dx++ {
		for dy := -radius; dy <= radius; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				p := cube.Pos{pos.X() + dx, pos.Y() + dy, pos.Z() + dz}
				name, _ := c.World().Block(p).EncodeBlock()
				if strings.Contains(name, "water") {
					return true
				}
			}
		}
	}
	return false
}

// waitForNearbyWater polls nearbyWaterFound until a water block appears near
// pos, the timeout elapses, or the context is cancelled.
func waitForNearbyWater(ctx context.Context, c *Client, pos cube.Pos, radius int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if nearbyWaterFound(c, pos, radius) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

func slotIsNoLongerWaterBucket(c *Client, slot int) bool {
	if c == nil || c.Screen == nil || slot < 0 || slot >= len(c.Screen.Inv.Slots()) {
		return false
	}
	stack, err := c.Screen.Inv.Item(slot)
	if err != nil || stack.Empty() {
		return false
	}
	name, _ := stack.Item().EncodeItem()
	return !isWaterBucketName(name)
}

func (c *Client) ensureWaterBucket(ctx context.Context, radius int) error {
	if slot, err := findWaterBucket(c); err == nil {
		c.Screen.SetCarriedItem(slot)
		return nil
	}

	emptySlot, err := findEmptyBucket(c)
	if err != nil {
		return err
	}
	sources := findNearbyWaterSources(c, radius)
	if len(sources) == 0 {
		return fmt.Errorf("no nearby water source found to refill bucket")
	}

	// Prefer a water source that has a reachable standing spot beside it.
	// Always picking the single closest source (as findNearbyWaterSource did)
	// meant that once the closest source turned out to be walled in by solid
	// blocks on every side, the bot retried that exact same unreachable
	// source forever, since the player's position (and therefore "closest")
	// never changed. Trying every candidate in distance order lets the bot
	// walk to a farther-but-reachable source instead.
	source := sources[0]
	var stand cube.Pos
	standOK := false
	for _, candidate := range sources {
		if s, ok := standingBeside(c, candidate); ok {
			source, stand, standOK = candidate, s, true
			break
		}
	}
	c.Logger.Infof("前往水源補水，位置=%v", source)

	c.Screen.SetCarriedItem(emptySlot)
	if !waitContext(ctx, 300*time.Millisecond) {
		return ctx.Err()
	}
	scoopFace := cube.FaceUp
	if standOK {
		c.WalkTo(mgl32.Vec3{float32(stand.X()) + 0.5, float32(stand.Y()), float32(stand.Z()) + 0.5})
		scoopFace = faceFromAdjacent(source, stand)
	}
	c.Logger.Infof("開始舀水，位置=%v", source)
	c.ScoopWater(source, scoopFace)
	for attempts := 1; attempts <= 3; attempts++ {
		if waitForWaterBucket(ctx, c, 3*time.Second) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempts == 3 {
			return fmt.Errorf("water bucket was not refilled from source %v after %d attempts", source, attempts)
		}
		c.Logger.Warnf("舀水未成功，重試第 %d 次，位置=%v", attempts+1, source)
		c.ScoopWater(source, scoopFace)
	}
	return nil
}

func waitForWaterBucket(ctx context.Context, c *Client, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := findWaterBucket(c); err == nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

func findCobblestoneStairWaterTargets(c *Client, center cube.Pos, radius int) []cube.Pos {
	if c.World() == nil {
		return nil
	}

	positions := make([]cube.Pos, 0)
	for x := center.X() - radius; x <= center.X()+radius; x++ {
		for y := center.Y() - radius; y <= center.Y()+radius; y++ {
			for z := center.Z() - radius; z <= center.Z()+radius; z++ {
				pos := cube.Pos{x, y, z}
				if c.World().Chunk(chunkPosFromBlockPos(pos)) == nil {
					continue
				}
				name, _ := c.World().Block(pos).EncodeBlock()
				if !strings.Contains(name, "air") {
					continue
				}
				if isAdjacentToCobblestoneStair(c, pos) && isWaterPlacementTarget(c, pos) {
					positions = append(positions, pos)
				}
			}
		}
	}
	return positions
}

func isAdjacentToCobblestoneStair(c *Client, pos cube.Pos) bool {
	if c.World() == nil {
		return false
	}
	for _, face := range []cube.Face{cube.FaceUp, cube.FaceDown, cube.FaceNorth, cube.FaceSouth, cube.FaceWest, cube.FaceEast} {
		adj := pos.Side(face)
		name, _ := c.World().Block(adj).EncodeBlock()
		if isCobblestoneStairName(name) {
			return true
		}
	}
	return false
}

func isCobblestoneStairName(name string) bool {
	return strings.Contains(name, "stairs") && (strings.Contains(name, "cobblestone") || strings.Contains(name, "stone"))
}

func filterWaterTargets(positions []cube.Pos, blockAt func(cube.Pos) string) []cube.Pos {
	filtered := make([]cube.Pos, 0, len(positions))
	for _, pos := range positions {
		name := blockAt(pos)
		if strings.Contains(name, "air") {
			filtered = append(filtered, pos)
		}
	}
	return filtered
}

func isWaterPlacementTarget(c *Client, pos cube.Pos) bool {
	if c.World() == nil {
		return false
	}
	name, _ := c.World().Block(pos).EncodeBlock()
	if !strings.Contains(name, "air") {
		return false
	}
	for _, face := range []cube.Face{cube.FaceUp, cube.FaceDown, cube.FaceNorth, cube.FaceSouth, cube.FaceWest, cube.FaceEast} {
		adj := pos.Side(face)
		if c.World() == nil {
			continue
		}
		adjName, _ := c.World().Block(adj).EncodeBlock()
		if strings.Contains(adjName, "air") || strings.Contains(adjName, "water") {
			continue
		}
		return true
	}
	return false
}

func waterPlacementFace(c *Client, pos cube.Pos) (cube.Pos, cube.Face, bool) {
	if c.World() == nil {
		return cube.Pos{}, 0, false
	}

	for _, face := range []cube.Face{cube.FaceUp, cube.FaceDown, cube.FaceNorth, cube.FaceSouth, cube.FaceWest, cube.FaceEast} {
		support := pos.Side(face)
		name, _ := c.World().Block(support).EncodeBlock()
		if strings.Contains(name, "air") || strings.Contains(name, "water") {
			continue
		}
		return support, face.Opposite(), true
	}
	return cube.Pos{}, 0, false
}

func isWaterBucketName(name string) bool {
	return strings.Contains(name, "water_bucket")
}

func findWaterBucket(c *Client) (int, error) {
	for slot, stack := range c.Screen.Inv.Slots()[:9] {
		if stack.Empty() {
			continue
		}
		name, _ := stack.Item().EncodeItem()
		if isWaterBucketName(name) {
			return slot, nil
		}
	}
	return -1, fmt.Errorf("no water bucket found in hotbar slots 0-8")
}

func findEmptyBucket(c *Client) (int, error) {
	for slot, stack := range c.Screen.Inv.Slots()[:9] {
		if stack.Empty() {
			continue
		}
		name, _ := stack.Item().EncodeItem()
		if name == "minecraft:bucket" {
			return slot, nil
		}
	}
	return -1, fmt.Errorf("no empty bucket found in hotbar slots 0-8")
}

func findNearbyWaterSource(c *Client, radius int) (cube.Pos, bool) {
	sources := findNearbyWaterSources(c, radius)
	if len(sources) == 0 {
		return cube.Pos{}, false
	}
	return sources[0], true
}

// findNearbyWaterSources returns every water source block within radius
// blocks of the player's current position, sorted from closest to farthest.
// Returning every candidate (rather than only the single closest one) lets
// callers skip sources that turn out to have no reachable standing spot
// instead of retrying the same unreachable source forever.
func findNearbyWaterSources(c *Client, radius int) []cube.Pos {
	if c.World() == nil || c.Self == nil {
		return nil
	}
	center := BlockPosFromVec3(c.Self.Position)
	var sources []cube.Pos
	for x := center.X() - radius; x <= center.X()+radius; x++ {
		for y := center.Y() - radius; y <= center.Y()+radius; y++ {
			for z := center.Z() - radius; z <= center.Z()+radius; z++ {
				pos := cube.Pos{x, y, z}
				if c.World().Chunk(chunkPosFromBlockPos(pos)) == nil {
					continue
				}
				name, _ := c.World().Block(pos).EncodeBlock()
				if isWaterSourceName(name) {
					sources = append(sources, pos)
				}
			}
		}
	}
	sort.Slice(sources, func(i, j int) bool {
		return distanceSquared(sources[i], center) < distanceSquared(sources[j], center)
	})
	return sources
}

func isWaterSourceName(name string) bool {
	return name == "minecraft:water"
}

func faceFromAdjacent(origin, adjacent cube.Pos) cube.Face {
	switch {
	case adjacent.X() > origin.X():
		return cube.FaceEast
	case adjacent.X() < origin.X():
		return cube.FaceWest
	case adjacent.Z() > origin.Z():
		return cube.FaceSouth
	case adjacent.Z() < origin.Z():
		return cube.FaceNorth
	default:
		return cube.FaceUp
	}
}

func allWaterBlocks(c *Client, positions []cube.Pos) bool {
	return waterFillCompleted(func(pos cube.Pos) string {
		if c.World() == nil {
			return ""
		}
		name, _ := c.World().Block(pos).EncodeBlock()
		return name
	}, positions)
}

func waterFillCompleted(blockAt func(cube.Pos) string, positions []cube.Pos) bool {
	if len(positions) == 0 {
		return true
	}
	for _, pos := range positions {
		if !strings.Contains(blockAt(pos), "water") {
			return false
		}
	}
	return true
}

func countWaterBlocks(c *Client, positions []cube.Pos) int {
	count := 0
	for _, pos := range positions {
		if c.World() == nil {
			continue
		}
		name, _ := c.World().Block(pos).EncodeBlock()
		if strings.Contains(name, "water") {
			count++
		}
	}
	return count
}
