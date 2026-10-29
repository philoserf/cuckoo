# cuckoo walkthrough

## Overview

cuckoo is a Go command that keeps the local time by sound alone. The pendulum tics and toks every second. The Westminster quarters play on the quarter hour, and on the hour all four quarters play, followed by one cuckoo call per hour on a twelve-hour dial. It prints nothing, and its one flag, `--quiet`, mutes the tic-tok but not the strikes.

It depends on two libraries. [oto](https://github.com/ebitengine/oto) opens the sound device and pulls samples from an `io.Reader`. [go-mp3](https://github.com/hajimehoshi/go-mp3) decodes the one recorded sound, the cuckoo's call, embedded from `cuckoo.mp3`. Everything else is synthesized at startup.

The only entry point is `main`. A second way in, `TestListen`, plays an hour through the speakers on demand (`task listen`) and is the only test that opens a sound device.

## Architecture

The program is one file, `main.go`, in `package main`, and its parts line up with the order in which a sound is made:

1. **Sounds** (`loadSounds`, `decodeCall`, `click`, `knock`, `rod`) render every sound once, at startup, into mono `[]float32` at 44.1 kHz.
2. **The loop** (`run`) wakes on every wall-clock second and calls `tick`.
3. **The striker** (`striker.strike`) decides whether that second opens a quarter, and what to strike.
4. **The strike layout** (`perform`, `quarter`) turns a strike into a list of sounds with start times, in seconds from now.
5. **The mixer** (`mixer`) holds scheduled sounds and, when oto asks, adds them into the next block of output.

Data flows one way: wall time → striker → layout → mixer → oto → speaker. There are two clocks. The wall clock decides what to play, and the mixer's count of frames handed to oto decides when it plays.

`main_test.go` covers everything below the device: striker sequences, quarter layouts, sound lengths and peaks, mixer arithmetic, and `tick` and `perform`. `run` and `main` are the untested part, and the coverage ratchet in `coverage.ratchet` holds exactly their statements as uncovered.

## Core walkthrough

### Starting up: `main` and `run`

`main` parses the one flag and hands off. `run` does all the work and returns only on an error or a signal.

`main.go` — `main`

```go
func main() {
	quiet := flag.Bool("quiet", false, "mute the pendulum's tic-tok")

	flag.Parse()

	err := run(*quiet)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cuckoo:", err)
		os.Exit(1)
	}
}
```

The first thing `run` does is render every sound, before it touches the device. Startup does all the expensive work so that playing later only adds.

`main.go` — `run`

```go
func run(quiet bool) error {
	snd, err := loadSounds()
	if err != nil {
		return err
	}
	...
```

### Rendering the sounds: `loadSounds`

`loadSounds` builds the `sounds` value that the rest of the program reads from. It holds the tic, the tok, the cuckoo's call, and four chime rods.

`main.go` — `sounds`

```go
type sounds struct {
	tic, tok, call []float32
	rods           [4][]float32
}
```

The rods are named by pitch. The constants double as their indices into `sounds.rods`, and `loadSounds` tunes each rod through a table keyed by those names:

`main.go` — `loadSounds`

```go
	pitches := [4]float64{gSharp4: 415.3, fSharp4: 369.99, e4: 329.63, b3: 246.94}

	var rods [4][]float32
	for i, frequency := range pitches {
		rods[i] = rod(frequency)
	}
```

Every synthesized sound goes through `render`, which samples a function of time at the shared rate for a given duration. The callback receives both the time in seconds and the sample index, since the rod's noise is keyed by index.

`main.go` — `render`

```go
func render(seconds float64, sample func(t float64, i int) float64) []float32 {
	out := make([]float32, max(1, int(rate*seconds)))
	for i := range out {
		out[i] = float32(sample(float64(i)/rate, i))
	}

	return out
}
```

The sounds are ported from a browser clock written against Web Audio, and their shaping keeps Web Audio's semantics. `expRamp` is `exponentialRampToValueAtTime`: it moves geometrically from one value to another over a span and holds at either end. `envelope` uses it to rise from `silence` (0.0001) to a peak and fall back.

`main.go` — `expRamp`

```go
func expRamp(from, to, t, span float64) float64 {
	if t <= 0 {
		return from
	}

	if t >= span {
		return to
	}

	return from * math.Pow(to/from, t/span)
}
```

`click`, the tic, is a 2140 Hz ping. Note the two decays. `math.Exp` shapes the tone itself, and `envelope` is the gain applied over it, the way a Web Audio buffer is shaped and then played through a gain node. `knock`, the tok, has the same structure at 620 Hz and 310 Hz.

`main.go` — `click`

```go
func click() []float32 {
	return render(0.022, func(t float64, _ int) float64 {
		tone := math.Sin(2*math.Pi*2140*t) * math.Exp(-t/0.0035) * 0.45

		return tone * envelope(t, 0.16, 0.003, 0.022)
	})
}
```

A rod is a sum of partials, each with its own amplitude and decay, plus a short hammer click. Each partial stops 20 ms after its decay ends. The fundamental decays over `ring`, which is why the rod is rendered `ring+0.02` seconds long.

`main.go` — `rod`

```go
	partials := []struct{ ratio, amp, decay float64 }{
		{1, 0.35, ring}, // the fundamental rings longest, and sets how long a rod sounds
		{2.003, 0.21, 1.5},
		...
	}

	return render(ring+0.02, func(t float64, i int) float64 {
		sum := 0.0

		for _, p := range partials {
			if t >= p.decay+0.02 {
				continue
			}
			...
```

The hammer's noise is not random. It is a hash of the sample index, so every rod gets the same click and every run renders the same samples. That is what lets `TestSounds` pin each sound's exact frame count.

`main.go` — `rod`

```go
		if t < 0.016 {
			hashed := math.Sin(float64(i)*12.9898) * 43758.5453
			noise := (hashed-math.Floor(hashed))*2 - 1
			sum += noise * math.Exp(-t/0.0022) * envelope(t, 0.28, 0.001, 0.016)
		}
```

The cuckoo's call is the one recorded sound. `decodeCall` decodes the embedded mp3 and refuses any rate other than 44.1 kHz, because nothing in the program resamples. It then keeps the first `callSeconds` (1.05 s) and averages the two channels to mono. It shapes the result with a 20 ms fade-in, a hold at 0.62, and a fade-out over the last 0.12 s.

`main.go` — `decodeCall`

```go
	if decoder.SampleRate() != rate {
		return nil, errSampleRate
	}
	...
	frames := min(len(stereo)/2, int(callSeconds*rate))
	duration := float64(frames) / rate
	release := max(0.06, duration-0.12)
	out := make([]float32, frames)
```

### Opening the device

Back in `run`, the sounds are ready and the device opens. The format here, stereo float32 at `rate`, is what `mixer.Read` writes, and `bytesPerFrame` (8) is its size per frame. `TestListen` repeats this setup line for line.

`main.go` — `run`

```go
	audio, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   rate,
		ChannelCount: 2,
		Format:       oto.FormatFloat32LE,
	})
	if err != nil {
		return fmt.Errorf("open audio: %w", err)
	}

	<-ready

	mix := &mixer{}
	player := audio.NewPlayer(mix)
	// oto would otherwise read half a second at a time, so a sound
	// scheduled now would be heard up to a second late.
	player.SetBufferSize(playerFrames * bytesPerFrame)
	player.Play()
```

The player reads from the mixer for as long as the program runs. `SetBufferSize` sets how much oto asks for in each read: `playerFrames` (2048 frames, about 46 ms). That size is the grid every sound lands on, because the mixer's clock only moves when oto reads.

### Taking bearings and starting the loop

`run` then creates the striker and gives it one reading before the loop starts. That first reading strikes nothing, so a clock started at 10:15:00.4 doesn't play the quarter it is already in.

`main.go` — `run`

```go
	clock := &striker{}
	clock.strike(time.Now())

	for {
		now := time.Now()
		timer := time.NewTimer(time.Until(now.Truncate(time.Second).Add(time.Second)))

		select {
		case <-ctx.Done():
			timer.Stop()

			// oto closes a player once it is collected, so it must stay
			// reachable for as long as the clock runs.
			runtime.KeepAlive(player)

			return nil
		case at := <-timer.C:
			tick(mix, &snd, clock, at, quiet)
		}
	}
```

Each pass sets a fresh timer for the next whole wall-clock second. It doesn't use a `time.Ticker`, so a late timer, a clock adjustment or a wake from sleep is corrected on the next pass. The `ctx.Done()` branch is the only way out, on Ctrl-C or SIGTERM. Its `runtime.KeepAlive(player)` is what keeps the player reachable until then: oto closes a player once the garbage collector reclaims it.

### One second: `tick`

`tick` is a beat of the pendulum, then a strike if this second opens a quarter. Even seconds tic and odd seconds tok. A beat is scheduled at `0`, meaning the mixer's current frame.

`main.go` — `tick`

```go
func tick(mix *mixer, snd *sounds, clock *striker, at time.Time, quiet bool) {
	if !quiet {
		beat := snd.tic
		if at.Second()%2 == 1 {
			beat = snd.tok
		}

		mix.schedule(beat, 0, groupTick)
	}

	chimes, cuckoos := clock.strike(at)
	if chimes > 0 {
		perform(mix, snd, chimes, cuckoos)
	}
}
```

The `chimes > 0` guard matters. Every strike replaces any strike still sounding, so calling `perform` with nothing to strike would cut off the chime that is ringing out.

### Deciding what to strike: `striker.strike`

The striker carries two pieces of state. `opened` records that the first reading has happened. `held` remembers the quarter boundary last struck (or held at startup) as "hour:minute".

`main.go` — `striker.strike`

```go
	hour, minute, second := at.Clock()
	boundary := second == 0 && minute%15 == 0
	key := strconv.Itoa(hour) + ":" + strconv.Itoa(minute)

	switch {
	case !s.opened:
		s.opened = true
		s.held = ""

		if boundary {
			s.held = key
		}

		return 0, 0
	case !boundary:
		s.held = ""

		return 0, 0
	case s.held == key:
		return 0, 0
	}

	s.held = key

	if minute > 0 {
		return minute / 15, 0
	}

	cuckoos := hour % 12
	if cuckoos == 0 {
		cuckoos = 12
	}

	return 4, cuckoos
```

A boundary is second 0 of minute 0, 15, 30 or 45 in local time. The first reading takes its bearings. Any reading off a boundary clears `held`. A boundary already held is not struck again. A quarter past strikes one phrase, half past two, and a quarter to three. The hour strikes all four, plus `hour % 12` cuckoos, with 0 counted as 12.

This transcript is from a command run while writing this document, not a live block. It reads one striker in order, starting at 10:14:58:

```text
strike(10:14:58) -> chimes 0, cuckoos 0
strike(10:14:59) -> chimes 0, cuckoos 0
strike(10:15:00) -> chimes 1, cuckoos 0
strike(10:15:00) -> chimes 0, cuckoos 0
strike(10:15:01) -> chimes 0, cuckoos 0
strike(10:59:59) -> chimes 0, cuckoos 0
strike(11:00:00) -> chimes 4, cuckoos 11
strike(11:00:01) -> chimes 0, cuckoos 0
strike(00:00:00) -> chimes 4, cuckoos 12
```

The first line is the bearings reading. The repeated 10:15:00 is held. The striker only strikes on a reading inside a boundary second. If the loop misses that second, for example while the machine sleeps, the quarter is skipped rather than played late.

### Laying out a strike: `perform` and `quarter`

`perform` asks `quarter` for the chime's notes, appends the cuckoo's calls after it, and hands the whole strike to the mixer in one call.

`main.go` — `perform`

```go
func perform(mix *mixer, snd *sounds, chimes, cuckoos int) {
	notes, total := quarter(chimes)
	strike := make([]cue, 0, len(notes)+cuckoos)

	for _, n := range notes {
		strike = append(strike, cue{samples: snd.rods[n.rod], at: n.at})
	}

	start := total + doorSeconds
	for call := range cuckoos {
		at := start + callLead + float64(call)*(float64(len(snd.call))/rate+callGap)
		strike = append(strike, cue{samples: snd.call, at: at})
	}

	mix.replace(groupStrike, strike)
}
```

`quarter` holds the Westminster chime as data: five phrases of four rods, and which phrases each quarter plays.

`main.go` — `quarter`

```go
	phrases := [][]int{
		{gSharp4, fSharp4, e4, b3},
		{e4, gSharp4, fSharp4, b3},
		{e4, fSharp4, gSharp4, e4},
		{gSharp4, e4, fSharp4, b3},
		{b3, fSharp4, gSharp4, e4},
	}
	quarters := [][]int{{0}, {1, 2}, {3, 4, 0}, {1, 2, 3, 4}}
	...
	for p, phrase := range quarters[min(count, len(quarters))-1] {
		for n, rod := range phrases[phrase] {
			last = float64(p)*phraseSeconds + float64(n)*noteSeconds
			notes = append(notes, note{at: chimeLead + last, rod: rod})
		}
	}

	return notes, chimeLead + last + ring
```

Notes within a phrase are `noteSeconds` (0.72 s) apart, and each phrase starts `phraseSeconds` (3.52 s) after the one before. The whole chime is offset by `chimeLead`. The second return value is when the last note has rung out, which is where the cuckoo's wait begins. This transcript, from the same scratch run, shows the half-hour layout (rods 0–3 are G♯4, F♯4, E4, B3):

```text
    0.05s rod 2
    0.77s rod 0
    1.49s rod 1
    2.21s rod 3
    3.57s rod 2
    4.29s rod 1
    5.01s rod 0
    5.73s rod 2
  rung out at 8.33s
```

For the hour, the cuckoo waits for the chime to ring out, then for `doorSeconds` (the doors opening, carried over from the visual clock), then `callLead`. Each call follows the last after its own length plus `callGap`. With a fresh mixer, three o'clock places its calls here:

```text
  call at frame 711333 (16.13s)
  call at frame 765576 (17.36s)
  call at frame 819819 (18.59s)
```

### Scheduling: `mixer.replace`, `schedule` and `add`

A `cue` is a sound and a start time in seconds from the mixer's clock. Once scheduled, it becomes a `voice` with an absolute start frame and a group.

`main.go` — `mixer.replace`

```go
func (m *mixer) replace(group int, cues []cue) {
	m.mu.Lock()
	defer m.mu.Unlock()

	kept := m.voices[:0]

	for _, v := range m.voices {
		if v.group != group {
			kept = append(kept, v)
		}
	}

	m.voices = kept

	for _, c := range cues {
		m.add(c, group)
	}
}
```

`replace` drops every voice in the group, cutting short a strike still sounding, and places the new cues, all under one lock. Every cue is measured from the same `m.pos`, so oto can't read between two notes and push the rest of the strike late. `schedule`, used for the beat, places a single cue the same way. Both convert seconds to an absolute frame in `add`:

`main.go` — `mixer.add`

```go
func (m *mixer) add(c cue, group int) {
	start := m.pos + int64(math.Round(c.at*rate))
	m.voices = append(m.voices, voice{samples: c.samples, start: start, group: group})
}
```

### Playing: `mixer.Read`

oto's goroutine calls `Read` whenever its buffer runs low, asking for `playerFrames` at a time. `Read` mixes every voice that overlaps the frames being handed over, drops voices that have finished, and advances `m.pos`. That count of frames is the mixer's clock.

`main.go` — `mixer.Read`

```go
	end := m.pos + int64(frames)
	kept := m.voices[:0]

	for _, v := range m.voices {
		stop := v.start + int64(len(v.samples))
		for i := max(v.start, m.pos); i < min(stop, end); i++ {
			mix[i-m.pos] += v.samples[i-v.start]
		}

		if stop > end {
			kept = append(kept, v)
		}
	}

	m.voices = kept
	m.pos = end
```

Then it clips the mono sum to [-1, 1] and writes each sample to both channels as little-endian float32:

`main.go` — `mixer.Read`

```go
	for i, s := range mix {
		bits := math.Float32bits(max(-1, min(1, s)))
		binary.LittleEndian.PutUint32(p[i*bytesPerFrame:], bits)
		binary.LittleEndian.PutUint32(p[i*bytesPerFrame+4:], bits)
	}

	return frames * bytesPerFrame, nil
```

`Read` always fills the whole buffer, with silence when nothing is scheduled, and never returns `io.EOF`. To oto it is an endless stream, and that is how the player keeps asking every 46 ms for as long as `run` loops.

### Shutting down

On Ctrl-C or SIGTERM, `signal.NotifyContext` cancels `ctx`. The loop stops its pending timer and holds the player one last time with `runtime.KeepAlive`, then `run` returns `nil`. Any sound still sounding is cut off as the process exits.
