// Copyright 2015 Matthew Collins
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"math"
	"runtime"
	"time"

	"github.com/thinkofdeath/steven/chat"
	"github.com/thinkofdeath/steven/protocol"
	"github.com/thinkofdeath/steven/render"
	"github.com/thinkofdeath/steven/type/direction"
	"github.com/thinkofdeath/steven/type/vmath"
)

const (
	playerHeight = 1.62
)

var Client = ClientState{
	Bounds: vmath.AABB{
		Min: vmath.Vector3{-0.3, 0, -0.3},
		Max: vmath.Vector3{0.3, 1.8, 0.3},
	},
}

type ClientState struct {
	X, Y, Z    float64
	Yaw, Pitch float64

	Jumping  bool
	VSpeed   float64
	KeyState [4]bool
	OnGround bool

	GameMode gameMode
	HardCore bool

	Bounds vmath.AABB

	positionText    *render.UIText
	directionText   *render.UIText
	memoryText      *render.UIText
	targetBlockText []*render.UIText

	fps       int
	frames    int
	lastCount time.Time
	fpsText   *render.UIText

	chat ChatUI
}

var memoryStats runtime.MemStats

func (c *ClientState) renderTick(delta float64) {
	c.frames++

	forward, yaw := c.calculateMovement()

	if c.GameMode.Fly() {
		c.X += forward * math.Cos(yaw) * -math.Cos(c.Pitch) * delta * 0.2
		c.Z -= forward * math.Sin(yaw) * -math.Cos(c.Pitch) * delta * 0.2
		c.Y -= forward * math.Sin(c.Pitch) * delta * 0.2
	} else if chunkMap[chunkPosition{int(c.X) >> 4, int(c.Z) >> 4}] != nil {
		c.X += forward * math.Cos(yaw) * delta * 0.1
		c.Z -= forward * math.Sin(yaw) * delta * 0.1
		if !c.OnGround {
			c.VSpeed -= 0.01 * delta
			if c.VSpeed < -0.3 {
				c.VSpeed = -0.3
			}
		} else if c.Jumping {
			c.VSpeed = 0.15
		} else {
			c.VSpeed = 0
		}
		c.Y += c.VSpeed * delta
	}

	if !c.GameMode.NoClip() {
		cy := c.Y
		cz := c.Z
		c.Y = render.Camera.Y - playerHeight
		c.Z = render.Camera.Z

		// We handle each axis separately to allow for a sliding
		// effect when pushing up against walls.

		bounds, _ := c.checkCollisions(c.Bounds)
		c.X = bounds.Min.X + 0.3
		c.copyToCamera()

		c.Z = cz
		bounds, _ = c.checkCollisions(c.Bounds)
		c.Z = bounds.Min.Z + 0.3
		c.copyToCamera()

		c.Y = cy
		bounds, _ = c.checkCollisions(c.Bounds)
		c.Y = bounds.Min.Y

		c.checkGround()
	}

	c.copyToCamera()

	// Temp displays

	c.positionText = setText(c.positionText,
		fmt.Sprintf("X: %.2f, Y: %.2f, Z: %.2f", c.X, c.Y, c.Z),
		5, 5, 255, 255, 255,
	)
	c.directionText = setText(c.directionText,
		fmt.Sprintf("Facing: %s", c.facingDirection()),
		5, 23, 255, 255, 255,
	)
	c.displayTargetInfo()

	runtime.ReadMemStats(&memoryStats)
	text := fmt.Sprintf("%s", formatMemory(memoryStats.Alloc))
	c.memoryText = setText(c.memoryText, text, 800-5-float64(render.SizeOfString(text)), 23, 255, 255, 255)

	now := time.Now()
	if now.Sub(c.lastCount) > time.Second {
		c.lastCount = now
		c.fps = c.frames
		c.frames = 0
	}
	text = fmt.Sprintf("FPS: %d", c.fps)
	c.fpsText = setText(c.fpsText, text, 800-5-float64(render.SizeOfString(text)), 5, 255, 255, 255)

	c.chat.render(delta)
}

func (c *ClientState) targetBlock() (x, y, z int, block Block) {
	const max = 4.0
	block = BlockAir.Base
	s := vmath.Vector3{c.X, c.Y + playerHeight, c.Z}
	d := c.viewVector()

	type gen struct {
		count   int
		base, d float64
	}
	newGen := func(start, d float64) *gen {
		g := &gen{}
		if d > 0 {
			g.base = (math.Ceil(start) - start) / d
		} else if d < 0 {
			d = math.Abs(d)
			g.base = (start - math.Floor(start)) / d
		}
		g.d = d
		return g
	}
	next := func(g *gen) float64 {
		g.count++
		if g.d == 0 {
			return math.Inf(1)
		}
		return g.base + float64(g.count-1)/g.d
	}

	aGen := newGen(s.X, d.X)
	bGen := newGen(s.Y, d.Y)
	cGen := newGen(s.Z, d.Z)
	prevN := 0.0
	nextNA := next(aGen)
	nextNB := next(bGen)
	nextNC := next(cGen)
	for {
		nextN := 0.0
		if nextNA < nextNB {
			if nextNA < nextNC {
				nextN = nextNA
				nextNA = next(aGen)
			} else {
				nextN = nextNC
				nextNC = next(cGen)
			}
		} else {
			if nextNB < nextNC {
				nextN = nextNB
				nextNB = next(bGen)
			} else {
				nextN = nextNC
				nextNC = next(cGen)
			}
		}
		if prevN == nextN {
			continue
		}
		if nextN > max {
			break
		}
		n := (prevN + nextN) / 2
		bx, by, bz := int(math.Floor(s.X+d.X*n)), int(math.Floor(s.Y+d.Y*n)), int(math.Floor(s.Z+d.Z*n))
		b := chunkMap.Block(bx, by, bz)
		if b.Collidable() {
			bb := b.CollisionBounds()
			for _, bound := range bb {
				bound.Shift(float64(bx), float64(by), float64(bz))
				if bound.IntersectsLine(s, d) {
					x, y, z = bx, by, bz
					block = b
					return
				}
			}
		}
		prevN = nextN
	}
	bx, by, bz := int(math.Floor(s.X+d.X*max)), int(math.Floor(s.Y+d.Y*max)), int(math.Floor(s.Z+d.Z*max))
	b := chunkMap.Block(bx, by, bz)
	if b.Collidable() {
		bb := b.CollisionBounds()
		for _, bound := range bb {
			bound.Shift(float64(bx), float64(by), float64(bz))
			if bound.IntersectsLine(s, d) {
				x, y, z = bx, by, bz
				block = b
				return
			}
		}
	}
	return
}

var debugStateColors = [...]chat.Color{
	cWhite:     chat.White,
	cOrange:    chat.Gold,
	cMagenta:   chat.LightPurple,
	cLightBlue: chat.Aqua,
	cYellow:    chat.Yellow,
	cLime:      chat.Green,
	cPink:      chat.Red,
	cGray:      chat.Gray,
	cSilver:    chat.DarkGray,
	cCyan:      chat.DarkAqua,
	cPurple:    chat.DarkPurple,
	cBlue:      chat.Blue,
	cBrown:     chat.Gold,
	cGreen:     chat.DarkGreen,
	cRed:       chat.DarkRed,
	cBlack:     chat.Black,
}

func (c *ClientState) displayTargetInfo() {
	for _, e := range c.targetBlockText {
		e.Free()
	}
	c.targetBlockText = c.targetBlockText[:0]
	tx, ty, tz, b := c.targetBlock()
	text := fmt.Sprintf("Target(%d,%d,%d)", tx, ty, tz)
	c.targetBlockText = append(c.targetBlockText, render.AddUIText(
		text,
		800-5-render.SizeOfString(text), 41, 255, 255, 255,
	))
	text = fmt.Sprintf("%s:%s", b.Plugin(), b.Name())
	c.targetBlockText = append(c.targetBlockText, render.AddUIText(
		text,
		800-5-render.SizeOfString(text), 59, 255, 255, 255,
	))

	for i, s := range b.states() {
		var r, g, b int = 255, 255, 255
		text = fmt.Sprint(s.Value)
		switch val := s.Value.(type) {
		case bool:
			b = 0
			if val {
				g = 255
				r = 0
			} else {
				r = 255
				g = 0
			}
		case color:
			r, g, b = chatColorRGB(debugStateColors[val])
		}
		pos := 800 - 5 - render.SizeOfString(text)
		c.targetBlockText = append(c.targetBlockText, render.AddUIText(
			text,
			pos, 59+18*(1+float64(i)), r, g, b,
		))
		text = fmt.Sprintf("%s=", s.Key)
		pos -= render.SizeOfString(text) + 2
		c.targetBlockText = append(c.targetBlockText, render.AddUIText(
			text,
			pos, 59+18*(1+float64(i)), 255, 255, 255,
		))
	}
}

func (c *ClientState) checkGround() {
	ground := vmath.AABB{
		Min: vmath.Vector3{-0.3, -0.05, -0.3},
		Max: vmath.Vector3{0.3, 0.0, 0.3},
	}
	_, c.OnGround = c.checkCollisions(ground)
}

func setText(txt *render.UIText, str string, x, y float64, rr, gg, bb int) *render.UIText {
	if txt != nil {
		txt.Free()
	}
	return render.AddUIText(str, x, y, rr, gg, bb)
}

func (c *ClientState) calculateMovement() (float64, float64) {
	forward := 0.0
	yaw := c.Yaw - math.Pi/2
	if c.KeyState[KeyForward] || c.KeyState[KeyBackwards] {
		forward = 1
		if c.KeyState[KeyBackwards] {
			yaw += math.Pi
		}
	}
	change := 0.0
	if c.KeyState[KeyLeft] {
		change = (math.Pi / 2) / (math.Abs(forward) + 1)
	}
	if c.KeyState[KeyRight] {
		change = -(math.Pi / 2) / (math.Abs(forward) + 1)
	}
	if c.KeyState[KeyRight] || c.KeyState[KeyLeft] {
		forward = 1
	}
	if c.KeyState[KeyBackwards] {
		yaw -= change
	} else {
		yaw += change
	}
	return forward, yaw
}

func (c *ClientState) facingDirection() direction.Type {
	viewVector := c.viewVector()
	for _, d := range direction.Values {
		if d.AsVector().Dot(viewVector) > 0.5 {
			return d
		}
	}
	return direction.Invalid
}

func (c *ClientState) viewVector() vmath.Vector3 {
	var viewVector vmath.Vector3
	viewVector.X = math.Cos(c.Yaw-math.Pi/2) * -math.Cos(c.Pitch)
	viewVector.Z = -math.Sin(c.Yaw-math.Pi/2) * -math.Cos(c.Pitch)
	viewVector.Y = -math.Sin(c.Pitch)
	return viewVector
}

func formatMemory(alloc uint64) string {
	const letters = "BKMG"
	i := 0
	for {
		check := alloc
		check >>= 10
		if check == 0 {
			break
		}
		alloc = check
		i++
	}
	l := string(letters[i])
	if l != "B" {
		l += "B"
	}
	return fmt.Sprintf("%d%s", alloc, l)
}

func (c *ClientState) checkCollisions(bounds vmath.AABB) (vmath.AABB, bool) {
	bounds.Shift(c.X, c.Y, c.Z)

	dir := &vmath.Vector3{
		X: -(render.Camera.X - c.X),
		Y: -(render.Camera.Y - playerHeight - c.Y),
		Z: -(render.Camera.Z - c.Z),
	}

	minX, minY, minZ := int(bounds.Min.X-1), int(bounds.Min.Y-1), int(bounds.Min.Z-1)
	maxX, maxY, maxZ := int(bounds.Max.X+1), int(bounds.Max.Y+1), int(bounds.Max.Z+1)

	hit := false
	for y := minY; y < maxY; y++ {
		for z := minZ; z < maxZ; z++ {
			for x := minX; x < maxX; x++ {
				b := chunkMap.Block(x, y, z)

				if b.Collidable() {
					for _, bb := range b.CollisionBounds() {
						bb.Shift(float64(x), float64(y), float64(z))
						if bb.Intersects(&bounds) {
							bounds.MoveOutOf(&bb, dir)
							hit = true
						}
					}
				}
			}
		}
	}
	return bounds, hit
}

func (c *ClientState) copyToCamera() {
	render.Camera.X = c.X
	render.Camera.Y = c.Y + playerHeight
	render.Camera.Z = c.Z
	render.Camera.Yaw = c.Yaw
	render.Camera.Pitch = c.Pitch
}

func (c *ClientState) tick() {
	// Now you may be wondering why we have to spam movement
	// packets (any of the Player* move/look packets) 20 times
	// a second instead of only sending when something changes.
	// This is because the server only ticks certain parts of
	// the player when a movement packet is recieved meaning
	// if we sent them any slower health regen would be slowed
	// down as well and various other things too (potions, speed
	// hack check). This also has issues if we send them too
	// fast as well since we will regen health at much faster
	// rates than normal players and some modded servers will
	// (correctly) detect this as cheating. Its Minecraft
	// what did you expect?
	// TODO(Think) Use the smaller packets when possible
	writeChan <- &protocol.PlayerPositionLook{
		X:        c.X,
		Y:        c.Y,
		Z:        c.Z,
		Yaw:      float32(-c.Yaw * (180 / math.Pi)),
		Pitch:    float32((-c.Pitch - math.Pi) * (180 / math.Pi)),
		OnGround: c.OnGround,
	}
}

type gameMode int

const (
	gmSurvival gameMode = iota
	gmCreative
	gmAdventure
	gmSpecator
)

func (g gameMode) Fly() bool {
	switch g {
	case gmCreative, gmSpecator:
		return true
	}
	return false
}

func (g gameMode) NoClip() bool {
	switch g {
	case gmSpecator:
		return true
	}
	return false
}

type teleportFlag byte

const (
	teleportRelX teleportFlag = 1 << iota
	teleportRelY
	teleportRelZ
	teleportRelYaw
	teleportRelPitch
)

func calculateTeleport(flag teleportFlag, flags byte, base, val float64) float64 {
	if flags&byte(flag) != 0 {
		return base + val
	}
	return val
}
