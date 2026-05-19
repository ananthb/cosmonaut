package daemon

import (
	"math/rand"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// progressScreen runs an on-theme animation while a long-running flow
// (codespace boot, SSH connect, editor launch) is in progress.
//
// The screen picks one of several animations on each invocation in a
// round-robin so the user sees variety across launches. All frames are
// 12 rows tall and 15 columns wide so the layout stays stable when one
// animation is swapped for another.
type progressScreen struct {
	ship   *widget.Label
	status *widget.Label
	canvas fyne.CanvasObject

	frames []string

	stopOnce sync.Once
	done     chan struct{}
}

func nextAnimation() []string {
	return animations[rand.Intn(len(animations))]
}

func newProgressScreen(message string) *progressScreen {
	frames := nextAnimation()
	ship := widget.NewLabelWithStyle(
		frames[0],
		fyne.TextAlignLeading,
		fyne.TextStyle{Monospace: true},
	)
	status := widget.NewLabel(message)
	status.Alignment = fyne.TextAlignCenter

	content := container.NewVBox(
		layout.NewSpacer(),
		container.NewCenter(ship),
		status,
		layout.NewSpacer(),
	)

	p := &progressScreen{
		ship:   ship,
		status: status,
		canvas: container.NewPadded(content),
		frames: frames,
		done:   make(chan struct{}),
	}
	go p.animate()
	return p
}

func (p *progressScreen) setStatus(msg string) {
	p.status.SetText(msg)
}

// stop halts the animation goroutine. Safe to call multiple times.
func (p *progressScreen) stop() {
	p.stopOnce.Do(func() { close(p.done) })
}

func (p *progressScreen) animate() {
	const frameDelay = 240 * time.Millisecond
	ticker := time.NewTicker(frameDelay)
	defer ticker.Stop()

	for i := 1; ; i = (i + 1) % len(p.frames) {
		select {
		case <-p.done:
			return
		case <-ticker.C:
		}
		frame := p.frames[i]
		fyne.Do(func() { p.ship.SetText(frame) })
	}
}

// animations is the catalog the progress screen rotates through.
var animations = [][]string{
	starshipFrames,
	rocketLaunchFrames,
	mountainFloatFrames,
	moonShotFrames,
	wobblyRocketFrames,
	arcadeShooterFrames,
}

// ── 1. Starship cruising through stars ─────────────────────────────
//
// Wide saucer with portholes, neck down to an engineering hull, two
// pylons spreading to a pair of warp nacelles that pulse between
// frames. The starfield scrolls down past the ship to suggest forward
// flight.
var starshipFrames = []string{
	`   *    .
    _______
   /       \
  /  o   o  \
   \_______/
       |
    [=====]
   //     \\
  /==\   /==\
.      *      .
   .      *
       .       `,
	`       .
    _______
   /       \
  /  o   o  \
   \_______/
       |
    [=====]
   //     \\
  /**\   /**\
*      .      *
.      *      .
   .      *    `,
	`*       .
    _______
   /       \
  /  o   o  \
   \_______/
       |
    [=====]
   //     \\
  /==\   /==\
       .
*      .      *
.      *      .`,
	`   .      *
    _______
   /       \
  /  o   o  \
   \_______/
       |
    [=====]
   //     \\
  /**\   /**\
   .      *
       .
*      .      *`,
}

// ── 2. Cute rocket lift-off ────────────────────────────────────────
//
// A small rocket sits on the pad, ignites, and climbs out of the
// canvas with a trailing exhaust. The loop "lands" the rocket back on
// the pad each cycle, but the rocket itself is endearing so it stays.
var rocketLaunchFrames = []string{
	`.      *      .
   *
      .
            *
  .
       *      .


      /\
     /||\
    /====\
  /==[||]==\   `,
	`.      *      .
   *
      .
            *
  .
       *      .

      /\
     /||\
    /====\
    \V||V/
  /==[||]==\   `,
	`.      *      .
   *
      .
            *
  .
       *      .
      /\
     /||\
    /====\
    \V||V/
      ::
  /========\   `,
	`.      *      .
   *
      .
            *
  .
      /\
     /||\
    /====\
    \V||V/
      ::
      ::
  /========\   `,
	`.      *      .
   *
      .
            *
      /\
     /||\
    /====\
    \V||V/
      ::
      ::
      ::
  /========\   `,
	`.      *      .
   *
      .
      /\
     /||\
    /====\
    \V||V/
      ::
      ::
      ::
      ::
  /========\   `,
	`.      *      .
   *
      /\
     /||\
    /====\
    \V||V/
      ::
      ::
      ::
      ::
      ::
  /========\   `,
	`.      *      .
      /\
     /||\
    /====\
    \V||V/
      ::
      ::
      ::
      ::
      ::
      ::
  /========\   `,
}

// ── 3. Stoic floating bust drifting over angular mountains ─────────
//
// A serene founder-figure floats over geometric peaks. Generic stoic
// bust silhouette, no specific likeness; mountains scroll left to
// suggest forward flight.
var mountainFloatFrames = []string{
	`       o
   *       .

    .---.
    |o o|
    | _ |
    '---'
   /|||\

   /\    /\
  /  \  /  \
==============`,
	`       o
       .
   *
     .---.
     |o o|
     | _ |
     '---'
    /|||\

  /\    /\
 /  \  /  \
=============_`,
	`       o
   .       *

      .---.
      |o o|
      | _ |
      '---'
     /|||\

 /\    /\    /
/  \  /  \  /
============__`,
	`       o
       *
   .
     .---.
     |o o|
     | _ |
     '---'
    /|||\

\    /\    /\
 \  /  \  /  \
===========___`,
}

// ── 4. Multi-stage moon-shot rocket ────────────────────────────────
//
// A tall stage-stacked rocket lifts off the pad with a growing exhaust
// trail, reaching toward the Moon at the top of the canvas. Loop
// restarts the launch so the screen reads as "moon shots, on repeat".
var moonShotFrames = []string{
	`         __
        /  \
        \__/


       /\
      /==\
     /====\
     |NASA|
     /||||\
    /======\
   ==[||||]==`,
	`         __
        /  \
        \__/


       /\
      /==\
     /====\
     |NASA|
     /||||\
    /======\
    *VVVVV*    `,
	`         __
        /  \
        \__/

       /\
      /==\
     /====\
     |NASA|
     /||||\
    /======\
    *VVVVV*
       ||      `,
	`         __
        /  \
        \__/
       /\
      /==\
     /====\
     |NASA|
     /||||\
    /======\
    *VVVVV*
       ||
       ::      `,
}

// ── 5. Wobbly top-heavy crewed rocket ──────────────────────────────
//
// A comically over-engineered rocket with two crew visible in a
// porthole, asymmetric boosters, and chaotic exhaust. The whole stack
// wobbles left and right between frames; the trajectory is, generously
// speaking, an aspiration.
var wobblyRocketFrames = []string{
	`  *      .   *
       .
   .       *
       *
       /\
      /==\
     |o  o|
     |    |
     /====\
    /||||||\
    ||||||||
     *VVVV*    `,
	`  *      .   *
       .
   .       *
       *
        /\
       /==\
      |o  o|
      |    |
      /====\
     /||||||\
     ||||||||
      *vVvV*   `,
	`  *      .   *
       .
   .       *
       *
      /\
     /==\
    |o  o|
    |    |
    /====\
   /||||||\
   ||||||||
    *VvVv*     `,
	`  *      .   *
       .
   .       *
       *
       /\
      /==\
     |o  o|
     |    |
     /====\
    /||||||\
    ||||||||
     ~VvVv~    `,
}

// ── 6. Pixel-arcade alien-shooter homage ───────────────────────────
//
// A formation of three rows of squat aliens shuffles right then back
// left while the lone defender at the bottom of the canvas fires a
// bullet up the centre column. On the fourth frame the bullet meets
// the middle alien in a small explosion, then the loop restarts and
// the next wave slides into formation.
var arcadeShooterFrames = []string{
	`*           *
  <O> <O> <O>
   <O> <O>
  <O> <O> <O>






       |
     [_^_]    `,
	` *         *
   <O> <O> <O>
    <O> <O>
   <O> <O> <O>



       |



     [_^_]    `,
	`   *      *
    <O> <O> <O>
     <O> <O>
    <O> <O> <O>
       |






     [_^_]    `,
	`*       *
   <O> *** <O>
    <O> <O>
   <O> <O> <O>







     [_^_]    `,
}
