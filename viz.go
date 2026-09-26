package main

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The field is a tiny Milkdrop/Winamp preset box: many plugins, randomized
// parameters, short holds, and wipes. It ticks faster than the sequencer so
// motion reads as animation instead of a slideshow, but kicks still punch it.

const vizTickEvery = 50 * time.Millisecond

// Cameras change when the agent plays a new lead note, but a camera stays up
// at least vizMinHold so bursts of events don't strobe, and with no events it
// drifts to the next one after vizDriftMin..vizDriftMax. All in 50 ms frames.
const (
	vizMinHold  = 80  // 4 s
	vizDriftMin = 300 // 15 s
	vizDriftMax = 500 // 25 s
)

// cue asks for the next camera; it cuts once the current one has had its time.
func (e *vizEngine) cue() { e.cued = true }

const (
	vizSpectrum = iota
	vizScope
	vizFire
	vizPlasma
	vizTunnel
	vizKaleido
	vizMatrix
	vizStars
	vizSpiral
	vizGlitch
	vizRings
	vizCube
	vizMoire
	vizHall
	vizGrid
	vizBars
	vizModeCount
)

// vizFeed dresses each plugin up as a camera in a closed-down pizzeria-arcade:
// the psychedelic plugins are what the tapes show after midnight.
type vizFeed struct {
	cam, room, plugin string
	palette           int
}

var vizFeeds = [vizModeCount]vizFeed{
	vizSpectrum: {"1A", "SHOW STAGE", "spectrum.dll", 0},
	vizScope:    {"1B", "DINING AREA", "scope.exe", 0},
	vizFire:     {"5", "BOILER ROOM", "inferno.avs", 1},
	vizPlasma:   {"1C", "PARTY ROOM", "plasma.milk", 3},
	vizTunnel:   {"2B", "SERVICE TUNNEL", "tunnel.milk", 2},
	vizKaleido:  {"7", "ARCADE", "kaleido.avs", 3},
	vizMatrix:   {"3", "SERVER CLOSET", "matrix.com", 0},
	vizStars:    {"6", "ROOF", "starfield.ape", 3},
	vizSpiral:   {"4A", "PLAYPLACE", "spiral.milk", 1},
	vizGlitch:   {"9", "▒▒▒▒▒▒", "glitch.exe", 2},
	vizRings:    {"4B", "SPEAKER ROOM", "rings.avs", 1},
	vizCube:     {"8", "STORAGE", "box3d.ape", 0},
	vizMoire:    {"1D", "BACKSTAGE", "moire.milk", 2},
	vizHall:     {"2A", "WEST HALL", "hall.cam", 4},
	vizGrid:     {"10", "PARKING LOT", "outrun.avs", 3},
	vizBars:     {"--", "NO SIGNAL", "test.pat", 3},
}

// Night-shift ramps: phosphor green, amber CRT, static magenta, synthwave
// dusk, and a dried-sepia "old tape" ramp for the horror feeds.
var vizPalettes = [][]string{
	{"16", "22", "28", "34", "40", "46", "47", "48", "82", "118", "154", "157"},
	{"16", "58", "94", "136", "172", "178", "214", "220", "221", "222", "228", "230"},
	{"16", "53", "90", "127", "164", "165", "201", "200", "199", "198", "213", "219"},
	{"16", "17", "54", "55", "91", "128", "165", "201", "206", "212", "51", "159"},
	{"16", "233", "52", "88", "94", "130", "137", "138", "180", "187", "223", "230"},
}

var vizStyles [][]lipgloss.Style

func init() {
	vizStyles = make([][]lipgloss.Style, len(vizPalettes))
	for i, pal := range vizPalettes {
		vizStyles[i] = make([]lipgloss.Style, len(pal))
		for j, c := range pal {
			vizStyles[i][j] = lipgloss.NewStyle().Foreground(lipgloss.Color(c))
		}
	}
}

type vizTickMsg time.Time

func scheduleViz() tea.Cmd {
	return tea.Tick(vizTickEvery, func(t time.Time) tea.Msg { return vizTickMsg(t) })
}

type vcell struct {
	ch rune
	ci int
}

type vstar struct {
	x, y, z float64
}

type vring struct {
	r    float64
	ci   int
	life int
}

type vizEngine struct {
	ready bool
	w, h  int

	mode, prevMode                     int
	hold                               int  // frames left before an idle drift to the next camera
	shown                              int  // frames the current camera has been on screen
	cued                               bool // a new lead note asked for the next camera
	blend, blendMax                    int
	wipe                               float64
	wipeKind                           int
	wild, crt, overlay, trails, chunky bool
	vhs, eyes                          bool
	mirror                             float64
	palette                            int

	// lowPower makes the feed brown out; set by the model as power drains.
	lowPower bool
	flick    float64 // hallway light, 1 = on
	msg      []string
	msgLeft  int

	frame, hue, seedInt, chaos int
	seed, level                float64
	p1, p2, p3, p4             float64
	step, lastStep, kickAge    int
	kick                       bool
	rotY, rotX                 float64

	grid, alt, prev []vcell
	heat, heatTmp   []float64
	peaks           []float64
	drops, dropSpd  []float64
	stars           []vstar
	rings           []vring
	rowShift        []int
	doTear          bool
	tearRow, tearBy int
}

func vizSize(width, height int) (int, int) {
	frameW, frameH := styleBox.GetFrameSize()
	w := width - frameW
	if w < 24 {
		w = 24
	}
	// header, beat bar, 4 log lines, separator, feed banner.
	const fixedRows = 8
	h := height - frameH - fixedRows
	if h < 6 {
		h = 6
	}
	return w, h
}

func (e *vizEngine) resize(w, h int) {
	e.w, e.h = w, h
	n := w * h
	e.grid = make([]vcell, n)
	e.alt = make([]vcell, n)
	e.prev = make([]vcell, n)
	e.heat = make([]float64, n)
	e.heatTmp = make([]float64, n)
	e.peaks = make([]float64, w)
	e.drops = make([]float64, w)
	e.dropSpd = make([]float64, w)
	e.rowShift = make([]int, h)
	e.stars = make([]vstar, max(16, w/2))
	for i := range e.stars {
		e.stars[i] = vstar{x: rand.Float64()*2 - 1, y: rand.Float64()*2 - 1, z: rand.Float64()}
	}
	for x := 0; x < w; x++ {
		e.drops[x] = rand.Float64() * float64(h)
		e.dropSpd[x] = 0.25 + rand.Float64()*0.7
	}
	e.rings = e.rings[:0]
}

func (e *vizEngine) relayout(w, h int) {
	if e.ready && e.w == w && e.h == h && len(e.grid) == w*h {
		return
	}
	e.resize(w, h)
	if !e.ready {
		e.ready = true
		e.hold = vizDriftMin
		e.lastStep = -1
		e.mode = rand.Intn(vizModeCount)
		e.prevMode = e.mode
		e.retune(false)
		e.palPick()
	}
	e.simulate()
	e.compose()
}

func (e *vizEngine) tick(w, h, step int, level float64, chaos int) {
	if w < 8 || h < 4 {
		return
	}
	if !e.ready || e.w != w || e.h != h {
		e.relayout(w, h)
	}
	e.frame++
	e.level = level
	e.chaos = chaos
	e.hue = (e.hue + 1 + chaos) % 12
	if e.msgLeft > 0 {
		e.msgLeft--
	}
	// The hallway light buzzes: mostly on, sometimes it drops for a few frames.
	switch {
	case e.flick < 1 && rand.Float64() < 0.3:
		e.flick = 1
	case rand.Float64() < 0.04+float64(chaos)*0.04:
		e.flick = rand.Float64() * 0.35
	}

	if step != e.lastStep {
		e.lastStep = step
		e.step = step
		e.kickAge = 0
		e.kick = step%4 == 0
		if e.kick {
			e.hue = (e.hue + 2) % 12
			e.rings = append(e.rings, vring{r: 0.12, ci: e.hue, life: 0})
			if len(e.rings) > 8 {
				e.rings = e.rings[len(e.rings)-8:]
			}
		}
	} else if e.kick {
		e.kickAge++
	}

	e.hold--
	e.shown++
	if e.hold <= 0 || (e.cued && e.shown >= vizMinHold) {
		e.switchMode()
	} else if e.frame%22 == 0 && e.blend == 0 {
		// Same plugin, new knobs. Keeps a mode from looping one formula.
		e.p1 = 0.35 + rand.Float64()*2.4
		e.p2 = 0.25 + rand.Float64()*2.0
		e.p3 = 0.4 + rand.Float64()*3
		e.p4 = rand.Float64()
		e.seed += 1.7
		if rand.Float64() < 0.4 {
			e.mirror = rand.Float64()
		}
	} else if e.frame%6 == 0 {
		e.p1 += (rand.Float64() - 0.5) * 0.05
		e.p2 += (rand.Float64() - 0.5) * 0.04
		e.seed += 0.07
	}
	if e.blend > 0 {
		e.wipe = 1 - float64(e.blend)/float64(max(e.blendMax, 1))
		e.blend--
		if e.wipe < 0.08 {
			e.wipe = 0.08
		}
	}

	e.doTear = rand.Float64() < 0.05+float64(chaos)*0.08
	e.tearRow = rand.Intn(h)
	e.tearBy = rand.Intn(7) - 3
	for y := range e.rowShift {
		e.rowShift[y] = 0
		if e.mode == vizGlitch && rand.Float64() < 0.35 {
			e.rowShift[y] = rand.Intn(9) - 4
		}
	}

	e.simulate()
	e.compose()
}

func (e *vizEngine) switchMode() {
	e.prevMode = e.mode
	for e.mode == e.prevMode {
		e.mode = rand.Intn(vizModeCount)
	}
	e.blendMax = 5 + rand.Intn(5)
	e.blend = e.blendMax
	e.wipe = 0.08
	// Most cuts are a camera switch: a burst of snow. The rest are Winamp wipes.
	e.wipeKind = wipeSnow
	if rand.Float64() < 0.4 {
		e.wipeKind = rand.Intn(wipeSnow)
	}
	e.hold = vizDriftMin + rand.Intn(vizDriftMax-vizDriftMin)
	e.shown, e.cued = 0, false
	e.retune(true)
	e.palPick()
}

func (e *vizEngine) retune(full bool) {
	e.seed = rand.Float64() * 80
	e.seedInt = rand.Int()
	e.p1 = 0.35 + rand.Float64()*2.4
	e.p2 = 0.25 + rand.Float64()*2.0
	e.p3 = 0.4 + rand.Float64()*3
	e.p4 = rand.Float64()
	e.mirror = rand.Float64()
	if full {
		e.wild = rand.Float64() < 0.38
		e.crt = rand.Float64() < 0.62
		e.overlay = rand.Float64() < 0.3 && e.mode != vizSpectrum
		e.trails = rand.Float64() < 0.72
		e.chunky = rand.Float64() < 0.5
		e.vhs = rand.Float64() < 0.35
		e.eyes = rand.Float64() < 0.3
	}
}

// announce puts a boxed caption over the feed for a few seconds.
func (e *vizEngine) announce(lines ...string) {
	e.msg = lines
	e.msgLeft = 100
}

// feed is the camera the viewer is looking at right now.
func (e *vizEngine) feed() vizFeed {
	if e.mode < 0 || e.mode >= vizModeCount {
		return vizFeed{cam: "--", room: "BOOT", plugin: "boot.avs"}
	}
	return vizFeeds[e.mode]
}

func (e *vizEngine) palPick() {
	e.palette = e.feed().palette
	// The hallway stays on old tape so it keeps reading as a room.
	if e.wild && e.mode != vizHall {
		e.hue = rand.Intn(12)
		e.palette = rand.Intn(len(vizPalettes))
	}
}

func (e *vizEngine) simulate() {
	e.simFire()
	e.simStars()
	e.simDrops()
	e.rotY += 0.16 + e.level*0.35 + e.pulse()*0.2
	e.rotX += 0.07 + e.p4*0.03
	for i := range e.rings {
		e.rings[i].r += 0.11 + e.level*0.08
		e.rings[i].life++
	}
	dst := e.rings[:0]
	for _, r := range e.rings {
		if r.r < 2.4 && r.life < 28 {
			dst = append(dst, r)
		}
	}
	e.rings = dst
}

func (e *vizEngine) pulse() float64 {
	if !e.kick {
		return 0
	}
	p := 1 - float64(e.kickAge)/4
	if p < 0 {
		return 0
	}
	return p
}

func (e *vizEngine) energy() float64 {
	v := 0.28 + e.level*2.4 + e.pulse()*0.45 + float64(e.chaos)*0.08
	return clamp(v, 0.2, 1.7)
}

func (e *vizEngine) simFire() {
	w, h := e.w, e.h
	if len(e.heat) != w*h {
		return
	}
	boost := e.energy()
	for x := 0; x < w; x++ {
		i := (h-1)*w + x
		if rand.Float64() < 0.42+e.level*0.45+e.pulse()*0.3 {
			e.heat[i] = 0.55 + rand.Float64()*0.45*boost
		} else {
			e.heat[i] *= 0.5
		}
	}
	for y := 0; y < h-1; y++ {
		for x := 0; x < w; x++ {
			xl, xr := x-1, x+1
			if xl < 0 {
				xl = 0
			}
			if xr >= w {
				xr = w - 1
			}
			sum := e.heat[(y+1)*w+xl] + e.heat[(y+1)*w+x] + e.heat[(y+1)*w+xr]
			if y+2 < h {
				sum += e.heat[(y+2)*w+x]
			} else {
				sum += e.heat[(y+1)*w+x]
			}
			e.heatTmp[y*w+x] = sum * 0.235
		}
	}
	copy(e.heatTmp[(h-1)*w:], e.heat[(h-1)*w:])
	e.heat, e.heatTmp = e.heatTmp, e.heat
}

func (e *vizEngine) simStars() {
	spd := 0.045 + e.level*0.05 + e.pulse()*0.03
	for i := range e.stars {
		e.stars[i].z -= spd
		if e.stars[i].z < 0.04 {
			e.stars[i] = vstar{x: rand.Float64()*2 - 1, y: rand.Float64()*2 - 1, z: 0.85 + rand.Float64()*0.15}
		}
	}
}

func (e *vizEngine) simDrops() {
	h := float64(e.h)
	for x := range e.drops {
		e.drops[x] += e.dropSpd[x] * (0.65 + e.level*1.4)
		if e.drops[x] > h+rand.Float64()*6 {
			e.drops[x] = -rand.Float64() * 4
			e.dropSpd[x] = 0.2 + rand.Float64()*0.9
		}
	}
}

const wipeSnow = 4

func (e *vizEngine) compose() {
	e.drawInto(e.grid, e.mode)
	switch {
	case e.blend > 0 && e.wipeKind == wipeSnow:
		e.snow(e.grid, clamp((1-e.wipe)*1.6, 0, 1))
	case e.blend > 0:
		e.drawInto(e.alt, e.prevMode)
		e.dissolve(e.grid, e.alt)
	case e.trails && sparseMode(e.mode):
		e.applyTrails()
	}
	e.post(e.grid)
	copy(e.prev, e.grid)
}

func sparseMode(mode int) bool {
	switch mode {
	case vizScope, vizStars, vizCube, vizRings:
		return true
	default:
		return false
	}
}

func (e *vizEngine) drawInto(g []vcell, mode int) {
	for i := range g {
		g[i] = vcell{}
	}
	if e.overlay && mode != vizSpectrum {
		e.drawSpectrum(g, 0.42, false)
	}
	switch mode {
	case vizSpectrum:
		e.drawSpectrum(g, 1, true)
	case vizScope:
		e.drawScope(g)
	case vizFire:
		e.drawFire(g)
	case vizPlasma:
		e.drawField(g, false, false)
	case vizTunnel:
		e.drawTunnel(g, false)
	case vizKaleido:
		e.drawTunnel(g, true)
	case vizMatrix:
		e.drawMatrix(g)
	case vizStars:
		e.drawStars(g)
	case vizSpiral:
		e.drawField(g, true, false)
	case vizGlitch:
		e.drawGlitch(g)
	case vizRings:
		e.drawRings(g)
	case vizCube:
		e.drawCube(g)
	case vizHall:
		e.drawHall(g)
	case vizGrid:
		e.drawGrid(g)
	case vizBars:
		e.drawBars(g)
	default:
		e.drawField(g, false, true)
	}
}

func (e *vizEngine) set(g []vcell, x, y int, ch rune, ci int) {
	if x < 0 || y < 0 || x >= e.w || y >= e.h || ch == 0 {
		return
	}
	ci %= 12
	if ci < 0 {
		ci += 12
	}
	g[y*e.w+x] = vcell{ch: ch, ci: ci}
}

func (e *vizEngine) drawSpectrum(g []vcell, gain float64, peaks bool) {
	w, h := e.w, e.h
	step := 1
	if e.chunky {
		step = 2
	}
	en := e.energy() * gain
	ff := float64(e.frame)
	for x := 0; x < w; x += step {
		u := float64(x) / float64(w)
		fx := u
		switch {
		case e.mirror > 0.72:
			fx = math.Abs(u-0.5) * 2
		case e.mirror > 0.4:
			fx = 1 - u
		}
		bin := math.Sin(fx*e.p1*7+ff*0.45+e.seed)*0.45 +
			math.Sin(fx*e.p2*15+ff*0.22)*0.3 +
			math.Sin(fx*e.p3*3+ff*0.7+e.seed)*0.25
		bin = (bin + 1) * 0.5
		env := 1 - fx*0.55
		ht := bin * env * en * float64(h)
		if ht > float64(h) {
			ht = float64(h)
		}
		for k := 0; k < step && x+k < w; k++ {
			col := x + k
			if peaks {
				if ht > e.peaks[col] {
					e.peaks[col] = ht
				} else {
					e.peaks[col] -= 0.28 + e.level*0.1
					if e.peaks[col] < 0 {
						e.peaks[col] = 0
					}
				}
			}
			for y := 0; y < h; y++ {
				fromBot := float64(h - 1 - y)
				if fromBot+1 <= ht {
					shade := int(fromBot / float64(h) * 11)
					e.set(g, col, y, '█', shade)
				} else if fromBot < ht {
					e.set(g, col, y, '▄', int(fromBot/float64(h)*11))
				} else if peaks && math.Abs(fromBot-e.peaks[col]) < 0.85 && e.peaks[col] > 0.4 {
					e.set(g, col, y, '▀', 11)
				}
			}
		}
	}
}

func (e *vizEngine) drawScope(g []vcell) {
	w, h := e.w, e.h
	en := e.energy()
	ff := float64(e.frame)
	if e.p4 > 0.55 {
		n := w * 3
		for i := 0; i < n; i++ {
			t := float64(i)/float64(n)*math.Pi*2 + ff*0.18
			x := math.Sin(t*math.Max(0.4, e.p1) + e.seed)
			y := math.Sin(t*math.Max(0.4, e.p2)+ff*0.05) * (0.55 + 0.45*math.Sin(t*0.5))
			col := int((x*en*0.85 + 1) * 0.5 * float64(w-1))
			row := int((y*en*0.9 + 1) * 0.5 * float64(h-1))
			ch := '·'
			if i%5 == 0 {
				ch = '*'
			}
			e.set(g, col, row, ch, (i/3+e.frame)%12)
		}
		return
	}
	for col := 0; col < w; col++ {
		fc := float64(col)
		y := math.Sin(fc*0.11*e.p1+ff*0.55+e.seed) + math.Sin(fc*0.04*e.p2+ff*0.2)*0.55
		y *= 0.45 * en
		row := int((y + 1.3) / 2.6 * float64(h-1))
		ci := (col/2 + e.frame) % 12
		e.set(g, col, row, '█', ci)
		e.set(g, col, row-1, '▀', ci)
		e.set(g, col, row+1, '▄', (ci+7)%12)
		ghost := math.Sin(fc*0.09*e.p3+ff*0.3+2) * 0.35 * en
		grow := int((ghost + 1.3) / 2.6 * float64(h-1))
		e.set(g, col, grow, '·', (ci+4)%12)
	}
}

func (e *vizEngine) drawFire(g []vcell) {
	w, h := e.w, e.h
	shades := []rune{' ', '·', '░', '▒', '▓', '█'}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := e.heat[y*w+x]
			if v < 0.05 {
				continue
			}
			if v > 1 {
				v = 1
			}
			si := int(v * float64(len(shades)-1))
			ci := int(v*8) + 3
			if ci > 11 {
				ci = 11
			}
			e.set(g, x, y, shades[si], ci)
		}
	}
}

// Terminal cells are about twice as tall as they are wide.
const cellAspect = 2.0

// unit is how many columns make 1.0 in feed coordinates: half the feed's longer
// side, measured in columns. Both axes share it, so circles stay round whether
// the feed is the wide full screen or pi's tall column.
func (e *vizEngine) unit() float64 {
	return math.Max(float64(e.w), float64(e.h)*cellAspect) / 2
}

// norm maps a cell to centred feed coordinates on that shared scale.
func (e *vizEngine) norm(x, y int) (float64, float64) {
	u := e.unit()
	return (float64(x) - float64(e.w)/2) / u, (float64(y) - float64(e.h)/2) * cellAspect / u
}

func (e *vizEngine) drawField(g []vcell, spiral, moire bool) {
	w, h := e.w, e.h
	ff := float64(e.frame)
	en := e.energy()
	shades := []rune(" .:-=+*#%@█")
	if e.chunky {
		shades = []rune(" ░▒▓█")
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := e.norm(x, y)
			if e.mirror > 0.5 {
				dx, dy = math.Abs(dx), math.Abs(dy)
			}
			var v float64
			switch {
			case moire:
				cx1 := math.Sin(ff*0.07) * 0.35
				cy1 := math.Cos(ff*0.05) * 0.35
				d1 := math.Hypot(dx-cx1, dy-cy1)
				d2 := math.Hypot(dx+cx1*0.6, dy-cy1)
				v = math.Sin(d1*e.p1*18-ff*0.45) + math.Sin(d2*e.p2*16+ff*0.32)
				v = v * 0.5
			case spiral:
				r := math.Hypot(dx, dy) + 0.05
				a := math.Atan2(dy, dx)
				v = math.Sin(a*e.p1*2 + math.Log(r)*e.p2*3 - ff*0.35)
				v += math.Sin(r*8*en-ff*0.5) * 0.35
			default:
				sway := math.Sin(float64(y)*0.45+ff*0.2) * e.p4 * 1.5
				fc := float64(x) + sway
				v = math.Sin(fc*0.08*e.p1+ff*0.33+e.seed) +
					math.Sin(float64(y)*0.55*e.p2-ff*0.21) +
					math.Sin((fc+float64(y))*0.07*e.p3+ff*0.17) +
					math.Sin(math.Hypot(dx, dy)*6*en-ff*0.4)
				v /= 4
			}
			v = clamp((v+1)*0.5, 0, 0.999)
			si := int(v * float64(len(shades)))
			if si >= len(shades) {
				si = len(shades) - 1
			}
			ci := int(v*12+float64(x+y)/7) % 12
			e.set(g, x, y, shades[si], ci)
		}
	}
}

func (e *vizEngine) drawTunnel(g []vcell, kaleido bool) {
	w, h := e.w, e.h
	ff := float64(e.frame)
	en := e.energy()
	shades := []rune(" ░▒▓█")
	folds := 3.0 + math.Floor(e.p4*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := e.norm(x, y)
			a := math.Atan2(dy, dx)
			r := math.Hypot(dx, dy)
			if kaleido {
				slice := math.Pi / folds
				a = math.Mod(math.Abs(a), slice*2)
				if a > slice {
					a = slice*2 - a
				}
				dx = math.Cos(a) * r
				dy = math.Sin(a) * r
			}
			pulse := 1 + e.pulse()*0.55
			v := math.Sin((3.2*pulse*en)/(r+0.18)-ff*0.5+e.seed) + math.Sin(a*e.p1+ff*0.28)
			v = clamp((v/2+1)*0.5, 0, 0.999)
			band := int(math.Mod((1/(r+0.2))*2+ff*0.15, 4))
			ch := shades[int(v*float64(len(shades)-1)+0.5)]
			if band == 0 && v > 0.3 {
				ch = '·'
			}
			e.set(g, x, y, ch, int(a*2+v*8+ff)%12)
		}
	}
}

func (e *vizEngine) drawMatrix(g []vcell) {
	w, h := e.w, e.h
	glyphs := []rune("0123456789ABCDEF#@$%&*")
	for x := 0; x < w; x++ {
		head := e.drops[x]
		for y := 0; y < h; y++ {
			d := head - float64(y)
			if d < 0 || d > 7 {
				continue
			}
			n := hash2(x, y, e.frame/2+e.seedInt)
			ch := glyphs[n%len(glyphs)]
			ci := 6
			switch {
			case d < 1:
				ch = '█'
				ci = 11
			case d < 2.2:
				ci = 9
			default:
				ci = 8 - int(d)
				if ci < 1 {
					ci = 1
				}
				if d > 4 {
					ch = '░'
				}
			}
			e.set(g, x, y, ch, ci)
		}
	}
}

func (e *vizEngine) drawStars(g []vcell) {
	w, h := e.w, e.h
	cx, cy := float64(w)/2, float64(h)/2
	for _, s := range e.stars {
		if s.z < 0.05 {
			continue
		}
		spread := e.unit() * 0.76
		px := cx + s.x/s.z*spread
		py := cy + s.y/s.z*spread/cellAspect
		col, row := int(px), int(py)
		b := 1 - s.z
		ch := '·'
		ci := 3
		switch {
		case b > 0.82:
			ch = '*'
			ci = 11
		case b > 0.6:
			ch = '+'
			ci = 8
		case b > 0.35:
			ch = '·'
			ci = 5
		}
		e.set(g, col, row, ch, ci)
		if b > 0.75 {
			e.set(g, col-1, row, '-', ci-2)
			e.set(g, col+1, row, '-', ci-2)
		}
	}
}

func (e *vizEngine) drawGlitch(g []vcell) {
	w, h := e.w, e.h
	noise := []rune(" ░▒▓█▀▄▌▐/\\+=*")
	for y := 0; y < h; y++ {
		shift := 0
		if y < len(e.rowShift) {
			shift = e.rowShift[y]
		}
		bar := hash2(y, e.frame/3, e.seedInt) % 12
		for x := 0; x < w; x++ {
			n := hash2(x+shift, y, e.frame+e.seedInt)
			ch := ' '
			ci := bar
			switch {
			case n%5 == 0:
				ch = noise[n%len(noise)]
				ci = n % 12
			case n%9 == 0:
				ch = '-'
				ci = (bar + 5) % 12
			case (x/4+y+e.frame)%6 == 0:
				ch = '░'
				ci = bar
			}
			if e.pulse() > 0.5 && y == (e.frame/2)%h {
				ch = '█'
				ci = 11
			}
			sx := x + shift
			e.set(g, sx, y, ch, ci)
		}
	}
}

func (e *vizEngine) drawRings(g []vcell) {
	w, h := e.w, e.h
	cx, cy := float64(w)/2, float64(h)/2
	// a little star dust so the field isn't empty between rings
	for i := 0; i < w; i += 3 {
		n := hash2(i, e.frame, e.seedInt)
		if n%4 == 0 {
			e.set(g, i, n%h, '·', n%12)
		}
	}
	u := e.unit()
	for _, rg := range e.rings {
		ci := rg.ci
		for x := 0; x < w; x++ {
			dx := (float64(x) - cx) / u
			// solve dy from radius, two branches
			rad := rg.r
			inner := rad*rad - dx*dx
			if inner < 0 {
				continue
			}
			dy := math.Sqrt(inner) * u / cellAspect // in rows
			e.set(g, x, int(cy-dy), '*', ci)
			e.set(g, x, int(cy+dy), 'o', (ci+6)%12)
		}
	}
}

func (e *vizEngine) drawCube(g []vcell) {
	w, h := e.w, e.h
	verts := [8][3]float64{
		{-1, -1, -1}, {1, -1, -1}, {1, 1, -1}, {-1, 1, -1},
		{-1, -1, 1}, {1, -1, 1}, {1, 1, 1}, {-1, 1, 1},
	}
	edges := [12][2]int{
		{0, 1}, {1, 2}, {2, 3}, {3, 0},
		{4, 5}, {5, 6}, {6, 7}, {7, 4},
		{0, 4}, {1, 5}, {2, 6}, {3, 7},
	}
	scale := (0.85 + 0.4*e.pulse() + e.level*0.45) * float64(min(w/2, h*2))
	var pts [8][2]int
	var depth [8]float64
	for i, v := range verts {
		x, y, z := v[0], v[1], v[2]
		cy, sy := math.Cos(e.rotY), math.Sin(e.rotY)
		x, z = x*cy+z*sy, -x*sy+z*cy
		cx, sx := math.Cos(e.rotX), math.Sin(e.rotX)
		y, z = y*cx-z*sx, y*sx+z*cx
		z += 3.4
		depth[i] = z
		pts[i][0] = int(float64(w)/2 + x/z*scale)
		pts[i][1] = int(float64(h)/2 + y/z*scale*0.55)
	}
	for i, ed := range edges {
		ci := 4 + i%8
		e.line(g, pts[ed[0]][0], pts[ed[0]][1], pts[ed[1]][0], pts[ed[1]][1], '+', ci)
	}
	for i, p := range pts {
		ch := '·'
		if depth[i] < 3.2 {
			ch = '#'
		}
		e.set(g, p[0], p[1], ch, 11)
	}
}

// drawHall is a one-point-perspective corridor: checker floor, wall tiles,
// buzzing ceiling lights, and a doorway at the end that is better left dark.
func (e *vizEngine) drawHall(g []vcell) {
	w, h := e.w, e.h
	vx, vy := float64(w-1)/2, float64(h-1)*0.42
	const far = 0.2 // the end wall is the frame scaled down to this
	walk := float64(e.frame) * (0.02 + e.level*0.03)
	light := e.flick + e.pulse()*0.15

	// at maps a cell onto the corridor: k is how far out from the vanishing
	// point it sits (1 at the frame edge), u/v are the lateral and vertical
	// position on that ring, and floorCeil says it is on the floor or ceiling.
	at := func(x, y int) (k, u, v float64, floorCeil bool) {
		fx, fy := float64(x)-vx, float64(y)-vy
		extY := vy
		if fy > 0 {
			extY = float64(h-1) - vy
		}
		kx, ky := math.Abs(fx)/vx, math.Abs(fy)/extY
		k = math.Max(kx, ky)
		if k == 0 {
			return 0, 0, 0, false
		}
		return k, fx / (vx * k), fy / (extY * k), ky >= kx
	}
	band := func(k float64) float64 { return math.Floor(0.7/k - walk*0.35) }

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			k, u, v, fc := at(x, y)
			if k < far {
				if k > far*0.86 {
					e.set(g, x, y, '▓', int(3*light))
				}
				continue
			}
			d := 1 / k // 1 at the frame edge, 1/far at the end wall
			ci := int(light * (1 - (d-1)/(1/far-1)*0.8) * 11)
			switch {
			case fc && v > 0: // floor
				check := int(math.Floor(d*2-walk)) + int(math.Floor((u+1)*4))
				ch := '░'
				if check%2 == 0 {
					ch = '▓'
				}
				e.set(g, x, y, ch, ci)
			case fc: // ceiling
				nk, _, _, _ := at(x, y+1)
				switch {
				case math.Abs(u) < 0.2 && frac(0.7*d-walk*0.35) < 0.14:
					e.set(g, x, y, '▀', ci+1)
				case nk > 0 && band(k) != band(nk):
					e.set(g, x, y, '·', ci-4)
				}
			default: // walls
				dir := 1
				if u > 0 {
					dir = -1
				}
				nk, _, _, _ := at(x+dir, y)
				seam := frac(0.7*d - walk*0.35)
				switch {
				case nk > 0 && band(k) != band(nk):
					e.set(g, x, y, '│', ci)
				case v > 0.35:
					e.set(g, x, y, '▒', ci-1)
				case v > 0.25:
					e.set(g, x, y, '═', ci)
				case v > -0.55 && v < -0.05 && seam > 0.35 && seam < 0.6:
					e.set(g, x, y, '░', ci-2) // old posters
				}
			}
		}
	}
	// Something in the doorway, only visible while the light is out.
	if e.eyes && light < 0.5 {
		ey := int(vy - vy*far*0.3)
		e.set(g, int(vx)-1, ey, '•', 11)
		e.set(g, int(vx)+1, ey, '•', 11)
	}
}

// drawGrid is the outrun horizon: striped sun, star sky, scrolling grid.
func (e *vizEngine) drawGrid(g []vcell) {
	w, h := e.w, e.h
	cx := float64(w-1) / 2
	hy := int(float64(h) * 0.5)
	r := float64(hy) * 0.85 * (1 + e.pulse()*0.06)
	for y := 0; y < hy; y++ {
		j := hy - y // rows above the horizon
		for x := 0; x < w; x++ {
			dx := (float64(x) - cx) / 2
			if math.Hypot(dx, float64(j)-0.5) < r {
				t := float64(j) / r // 1 at the top of the sun, 0 at the horizon
				// Stripe cuts scroll down and get fatter toward the horizon.
				cut := (j + e.frame/4) % 3
				if (t < 0.65 && cut == 0) || (t < 0.3 && cut == 1) {
					continue
				}
				e.set(g, x, y, '█', 5+int(t*6))
				continue
			}
			if n := hash2(x, y, e.seedInt); n%37 == 0 {
				ch := '·'
				if (n/37+e.frame/6)%5 == 0 {
					ch = '*'
				}
				e.set(g, x, y, ch, 4+n%6)
			}
		}
	}
	for x := 0; x < w; x++ {
		e.set(g, x, hy, '─', 11)
	}
	scroll := float64(e.frame) * (0.05 + e.level*0.08)
	depth := func(y int) float64 { return 3 / (float64(y-hy) / float64(h-hy)) }
	gap := float64(w) * 0.09 * (1 + e.p4)
	for y := hy + 1; y < h; y++ {
		t := float64(y-hy) / float64(h-hy)
		ci := 4 + int(t*7)
		// Only draw a rung where they are at least a row apart; closer to the
		// horizon they would smear into a solid block.
		d0, d1 := depth(y), depth(y+1)
		if d0-d1 < 1 && math.Floor(d0+scroll) != math.Floor(d1+scroll) {
			for x := 0; x < w; x++ {
				e.set(g, x, y, '─', ci)
			}
		}
		for k := -12; k <= 12; k++ {
			x := int(cx + float64(k)*gap*t*2)
			ch := '|'
			switch {
			case k < 0:
				ch = '/'
			case k > 0:
				ch = '\\'
			}
			e.set(g, x, y, ch, ci)
		}
	}
}

// testBars are the colour steps of an old test card, as palette shades.
var testBars = []int{11, 9, 10, 6, 8, 4, 7}

// drawBars is a dead channel: test-card bars, a reversed strip, then snow.
func (e *vizEngine) drawBars(g []vcell) {
	w, h := e.w, e.h
	split := h * 3 / 5
	strip := split + max(1, h/10)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			b := x * len(testBars) / w
			switch {
			case y < split:
				e.set(g, x, y, '█', testBars[b])
			case y < strip:
				if b%2 == 0 {
					e.set(g, x, y, '█', testBars[len(testBars)-1-b])
				}
			default:
				n := hash2(x, y, e.frame+e.seedInt)
				e.set(g, x, y, []rune(" ░▒▓·")[n%5], 2+n%6)
			}
		}
	}
	roll := e.frame % (h + 4)
	for x := 0; x < w; x++ {
		e.set(g, x, roll-2, '▒', 3)
	}
	if (e.frame/10)%3 != 0 {
		e.caption(g, []string{"NO SIGNAL"})
	}
}

// caption draws a double-lined box of text in the middle of the feed.
func (e *vizEngine) caption(g []vcell, lines []string) {
	inner := 0
	for _, l := range lines {
		inner = max(inner, len([]rune(l)))
	}
	inner += 4
	if inner+2 > e.w || len(lines)+2 > e.h {
		return
	}
	x0 := (e.w - inner - 2) / 2
	y0 := (e.h - len(lines) - 2) / 2
	row := func(y int, l, fill, r rune, text string) {
		e.set(g, x0, y, l, 11)
		t := []rune(text)
		pad := (inner - len(t)) / 2
		for i := 0; i < inner; i++ {
			ch := fill
			if j := i - pad; j >= 0 && j < len(t) {
				ch = t[j]
			}
			g[y*e.w+x0+1+i] = vcell{ch: ch, ci: 11}
		}
		e.set(g, x0+inner+1, y, r, 11)
	}
	row(y0, '╔', '═', '╗', "")
	for i, l := range lines {
		row(y0+1+i, '║', ' ', '║', l)
	}
	row(y0+len(lines)+1, '╚', '═', '╝', "")
}

// snow replaces a share of the frame with static, like flipping cameras.
func (e *vizEngine) snow(g []vcell, density float64) {
	noise := []rune(" ·░▒▓")
	for i := range g {
		n := hash2(i, e.frame, e.seedInt)
		if float64(n%1000)/1000 < density {
			g[i] = vcell{ch: noise[n%len(noise)], ci: 3 + n%7}
		}
	}
}

func frac(v float64) float64 { return v - math.Floor(v) }

func (e *vizEngine) line(g []vcell, x0, y0, x1, y1 int, ch rune, ci int) {
	dx := absInt(x1 - x0)
	dy := -absInt(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for n := 0; n < e.w+e.h+8; n++ {
		e.set(g, x0, y0, ch, ci)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func (e *vizEngine) dissolve(dst, src []vcell) {
	for y := 0; y < e.h; y++ {
		for x := 0; x < e.w; x++ {
			if !e.showNew(x, y) {
				dst[y*e.w+x] = src[y*e.w+x]
			}
		}
	}
}

func (e *vizEngine) showNew(x, y int) bool {
	switch e.wipeKind {
	case 1:
		return float64(e.w-x)/float64(e.w) < e.wipe
	case 2:
		dx := float64(x)/float64(e.w) - 0.5
		dy := float64(y)/float64(e.h) - 0.5
		return math.Hypot(dx, dy)*1.7 < e.wipe
	case 3:
		return float64(hash2(x, y, 9)%1000)/1000 < e.wipe
	default:
		return float64(x)/float64(e.w) < e.wipe+float64(hash2(x, y, 3)%100)/800
	}
}

func (e *vizEngine) applyTrails() {
	for i := range e.grid {
		if e.grid[i].ch != 0 && e.grid[i].ch != ' ' {
			continue
		}
		p := e.prev[i]
		if p.ch == 0 || p.ch == ' ' || p.ci <= 0 {
			continue
		}
		e.grid[i] = vcell{ch: fadeChar(p.ch), ci: p.ci - 1}
	}
}

func fadeChar(r rune) rune {
	switch r {
	case '█', '#', '*':
		return '▓'
	case '▓', '+':
		return '▒'
	case '▒':
		return '░'
	case '░', '▄', '▀':
		return '·'
	default:
		return '·'
	}
}

func (e *vizEngine) post(g []vcell) {
	if e.crt {
		for y := 1; y < e.h; y += 2 {
			for x := 0; x < e.w; x++ {
				c := &g[y*e.w+x]
				switch c.ch {
				case '█':
					c.ch = '▓'
				case '▓':
					c.ch = '▒'
				}
				if c.ci > 0 {
					c.ci--
				}
			}
		}
	}
	if e.doTear {
		e.shiftRow(g, e.tearRow, e.tearBy)
	}
	// VHS tracking: a band of noise rolls down the tape.
	if e.vhs {
		band := e.frame%(e.h+6) - 3
		for y := band - 1; y <= band+1; y++ {
			if y < 0 || y >= e.h {
				continue
			}
			e.shiftRow(g, y, rand.Intn(5)-2)
			for x := 0; x < e.w; x++ {
				if n := hash2(x, y, e.frame); n%3 == 0 {
					g[y*e.w+x] = vcell{ch: '▒', ci: 2 + n%4}
				}
			}
		}
	}
	// Low power: the feed browns out now and then.
	if e.lowPower && rand.Float64() < 0.08 {
		for i := range g {
			g[i].ci = max(0, g[i].ci-6)
		}
	}
	if e.chaos >= 2 && e.frame%2 == 0 {
		e.shiftRow(g, e.frame%e.h, 2)
	}
	if e.pulse() > 0.75 {
		y := e.h / 2
		for x := 0; x < e.w; x += 2 {
			c := &g[y*e.w+x]
			if c.ch == 0 || c.ch == ' ' {
				c.ch = '·'
			}
			c.ci = 11
		}
	}
	if e.msgLeft > 0 && (e.msgLeft > 30 || e.msgLeft%4 < 2) {
		e.caption(g, e.msg)
	}
}

func (e *vizEngine) shiftRow(g []vcell, y, by int) {
	if y < 0 || y >= e.h || by == 0 {
		return
	}
	row := make([]vcell, e.w)
	copy(row, g[y*e.w:(y+1)*e.w])
	for x := 0; x < e.w; x++ {
		src := (x - by) % e.w
		if src < 0 {
			src += e.w
		}
		g[y*e.w+x] = row[src]
	}
}

func (e vizEngine) render() string {
	if e.w == 0 || len(e.grid) != e.w*e.h {
		return ""
	}
	body := e.paint(e.grid)
	return e.banner() + "\n" + body
}

func (e vizEngine) banner() string {
	f := e.feed()
	tag := "LIVE"
	switch {
	case e.blend > 0 && e.wipeKind == wipeSnow:
		tag = "SWITCHING"
	case e.blend > 0:
		tag = "MIX"
	case e.lowPower:
		tag = "LOW PWR"
	}
	left := fmt.Sprintf("CAM %-2s  %s", f.cam, f.room)
	right := fmt.Sprintf("%s  %s", f.plugin, tag)
	gap := e.w - len([]rune(left)) - len(right)
	if gap < 2 {
		return vizStyles[e.palette%len(vizStyles)][10].Render(fitWidth(left, e.w))
	}
	return vizStyles[e.palette%len(vizStyles)][10].Render(left + strings.Repeat(" ", gap) + right)
}

// sceneMode feeds draw a place with depth shading, so hue crawl would smear it.
func sceneMode(mode int) bool {
	return mode == vizHall || mode == vizGrid || mode == vizBars
}

func (e vizEngine) paint(g []vcell) string {
	styles := vizStyles[e.palette%len(vizStyles)]
	shift := e.hue
	switch {
	case sceneMode(e.mode) && e.blend == 0:
		shift = 0
	case e.mode == vizSpectrum && e.blend == 0 && !e.wild:
		shift = e.hue / 3
	}
	lines := make([]string, e.h)
	for y := 0; y < e.h; y++ {
		var b strings.Builder
		row := g[y*e.w : (y+1)*e.w]
		for x := 0; x < e.w; {
			ch := row[x].ch
			if ch == 0 || ch == ' ' {
				j := x + 1
				for j < e.w && (row[j].ch == 0 || row[j].ch == ' ') {
					j++
				}
				b.WriteString(strings.Repeat(" ", j-x))
				x = j
				continue
			}
			ci := (row[x].ci + shift) % 12
			j := x + 1
			seg := []rune{ch}
			for j < e.w && row[j].ch != 0 && row[j].ch != ' ' && (row[j].ci+shift)%12 == ci {
				seg = append(seg, row[j].ch)
				j++
			}
			b.WriteString(styles[ci].Render(string(seg)))
			x = j
		}
		lines[y] = b.String()
	}
	return strings.Join(lines, "\n")
}

func fitWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) >= w {
		return string(r[:w])
	}
	return s + strings.Repeat(" ", w-len(r))
}

func hash2(a, b, c int) int {
	h := uint32(a)*374761393 + uint32(b)*668265263 + uint32(c)*2246822519
	h = (h ^ (h >> 13)) * 1274126177
	return int(h & 0x7fffffff)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
