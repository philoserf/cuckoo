package main

import (
	"encoding/binary"
	"math"
	"os"
	"runtime"
	"testing"
	"time"
)

func at(hour, minute, second int) time.Time {
	return time.Date(2026, 10, 12, hour, minute, second, 0, time.UTC)
}

func TestStrike(t *testing.T) {
	t.Parallel()

	// One striker read in order: the sequence is the test.
	clock := &striker{}
	steps := []struct {
		hour, minute, second, chimes, cuckoos int
	}{
		{10, 15, 0, 0, 0}, // the first reading only takes its bearings
		{10, 15, 0, 0, 0}, // a held boundary is not struck again
		{10, 15, 1, 0, 0},
		{10, 15, 0, 1, 0},
		{10, 30, 0, 2, 0},
		{10, 45, 0, 3, 0},
		{11, 0, 0, 4, 11},
		{12, 0, 0, 4, 12},
		{0, 0, 0, 4, 12},
		{13, 1, 0, 0, 0},
		{20, 0, 0, 4, 8},
	}

	for _, step := range steps {
		chimes, cuckoos := clock.strike(at(step.hour, step.minute, step.second))
		if chimes != step.chimes || cuckoos != step.cuckoos {
			t.Fatalf("%02d:%02d:%02d struck %d chimes, %d cuckoos; want %d, %d",
				step.hour, step.minute, step.second, chimes, cuckoos, step.chimes, step.cuckoos)
		}
	}
}

func TestStrikeOpensOffBoundary(t *testing.T) {
	t.Parallel()

	clock := &striker{}
	clock.strike(at(10, 14, 59))

	chimes, _ := clock.strike(at(10, 15, 0))
	if chimes != 1 {
		t.Fatalf("struck %d chimes at 10:15 after opening at 10:14:59; want 1", chimes)
	}
}

func TestQuarter(t *testing.T) {
	t.Parallel()

	const gs, fs, e, b = gSharp4, fSharp4, e4, b3

	tests := []struct {
		name  string
		rods  []int
		count int
	}{
		{name: "first", count: 1, rods: []int{gs, fs, e, b}},
		{name: "second", count: 2, rods: []int{e, gs, fs, b, e, fs, gs, e}},
		{name: "third", count: 3, rods: []int{gs, e, fs, b, b, fs, gs, e, gs, fs, e, b}},
		{name: "hour", count: 4, rods: []int{e, gs, fs, b, e, fs, gs, e, gs, e, fs, b, b, fs, gs, e}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			notes, total := quarter(tc.count)
			if len(notes) != len(tc.rods) {
				t.Fatalf("got %d notes, want %d", len(notes), len(tc.rods))
			}

			for i, n := range notes {
				if n.rod != tc.rods[i] {
					t.Fatalf("note %d strikes rod %d, want %d", i, n.rod, tc.rods[i])
				}

				want := chimeLead + float64(i/4)*phraseSeconds + float64(i%4)*noteSeconds
				if math.Abs(n.at-want) > 1e-9 {
					t.Fatalf("note %d at %v s, want %v s", i, n.at, want)
				}
			}

			if want := notes[len(notes)-1].at + ring; math.Abs(total-want) > 1e-9 {
				t.Fatalf("total %v s, want %v s", total, want)
			}
		})
	}
}

func TestExpRamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		t, want, span float64
	}{
		{name: "before", t: -1, span: 1, want: 0.0001},
		{name: "start", t: 0, span: 1, want: 0.0001},
		{name: "middle", t: 0.5, span: 1, want: 0.01},
		{name: "end", t: 1, span: 1, want: 1},
		{name: "after", t: 2, span: 1, want: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := expRamp(0.0001, 1, tc.t, tc.span); math.Abs(got-tc.want) > 1e-12 {
				t.Fatalf("expRamp at %v = %v, want %v", tc.t, got, tc.want)
			}
		})
	}
}

func TestSounds(t *testing.T) {
	t.Parallel()

	snd, err := loadSounds()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		samples []float32
		frames  int
	}{
		{name: "tic", samples: snd.tic, frames: 970},
		{name: "tok", samples: snd.tok, frames: 1323},
		{name: "rod", samples: snd.rods[0], frames: 115542},
		{name: "call", samples: snd.call, frames: 46305},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.samples) != tc.frames {
				t.Fatalf("%d frames, want %d", len(tc.samples), tc.frames)
			}

			loudest := float32(0)
			for _, s := range tc.samples {
				loudest = max(loudest, s, -s)
			}

			if loudest == 0 || loudest > 1 {
				t.Fatalf("peak %v, want within (0, 1]", loudest)
			}
		})
	}
}

func TestDecodeCallRejectsNoise(t *testing.T) {
	t.Parallel()

	_, err := decodeCall([]byte("not an mp3"))
	if err == nil {
		t.Fatal("decoded garbage without an error")
	}
}

func frames(t *testing.T, mix *mixer, count int) []float32 {
	t.Helper()

	buf := make([]byte, count*bytesPerFrame)

	n, err := mix.Read(buf)
	if err != nil || n != len(buf) {
		t.Fatalf("Read = %d, %v; want %d, nil", n, err, len(buf))
	}

	out := make([]float32, count)

	for i := range out {
		left := math.Float32frombits(binary.LittleEndian.Uint32(buf[i*bytesPerFrame:]))
		right := math.Float32frombits(binary.LittleEndian.Uint32(buf[i*bytesPerFrame+4:]))

		if left != right {
			t.Fatalf("frame %d: left %v, right %v", i, left, right)
		}

		out[i] = left
	}

	return out
}

func equal(got, want []float32) bool {
	if len(got) != len(want) {
		return false
	}

	for i := range got {
		if math.Abs(float64(got[i]-want[i])) > 1e-6 {
			return false
		}
	}

	return true
}

func TestMixer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup func(m *mixer)
		name  string
		want  []float32
	}{
		{
			name: "overlapping voices add",
			setup: func(m *mixer) {
				m.schedule(0, []float32{0.1, 0.1, 0.1})
				m.schedule(1, []float32{0.2, 0.2})
			},
			want: []float32{0.1, 0.3, 0.3, 0, 0},
		},
		{
			name: "replace cuts short only the strike",
			setup: func(m *mixer) {
				m.schedule(0, []float32{0.1, 0.1})
				m.replace(0, []cue{{samples: []float32{0.5, 0.5}, at: 0}})
				m.replace(1, []cue{{samples: []float32{0.2}, at: 0}})
			},
			want: []float32{0.1, 0.3, 0, 0, 0},
		},
		{
			name: "loud sums clip",
			setup: func(m *mixer) {
				m.schedule(0, []float32{0.8})
				m.schedule(0, []float32{0.8})
				m.schedule(2, []float32{-0.9, -0.9})
				m.schedule(2, []float32{-0.9, -0.9})
			},
			want: []float32{1, 0, -1, -1, 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mix := &mixer{}
			tc.setup(mix)

			if got := frames(t, mix, len(tc.want)); !equal(got, tc.want) {
				t.Fatalf("mixed %v, want %v", got, tc.want)
			}

			if len(mix.voices) != 0 {
				t.Fatalf("%d voices left after they ended", len(mix.voices))
			}
		})
	}
}

func TestMixerSpansReads(t *testing.T) {
	t.Parallel()

	mix := &mixer{}
	mix.schedule(1, []float32{0.1, 0.2, 0.3, 0.4})

	if got := frames(t, mix, 3); !equal(got, []float32{0, 0.1, 0.2}) {
		t.Fatalf("first read %v", got)
	}

	if len(mix.voices) != 1 {
		t.Fatal("dropped a voice still sounding")
	}

	if got := frames(t, mix, 3); !equal(got, []float32{0.3, 0.4, 0}) {
		t.Fatalf("second read %v", got)
	}

	// A sound scheduled for a frame handed over already starts now.
	mix.schedule(0, []float32{0.5})

	if got := frames(t, mix, 1); !equal(got, []float32{0.5}) {
		t.Fatalf("third read %v", got)
	}
}

// device reads mix as oto does, a player buffer at a time, each read up to
// 30 ms late, on a clock that runs slow by drift.
type device struct {
	mix   *mixer
	start time.Time
	drift float64
	reads int
}

// until hands over every read due before when, and returns the time of the
// last one.
func (d *device) until(when time.Time) time.Time {
	var now time.Time

	for {
		due := float64(d.reads*playerFrames) / (rate * (1 - d.drift))
		late := float64(d.reads*7919%30) / 1000

		next := d.start.Add(time.Duration((due + late) * float64(time.Second)))
		if !next.Before(when) {
			return now
		}

		now = next
		d.reads++
		d.mix.pos += playerFrames
		d.mix.observe(now)
	}
}

func TestFrameKeepsTheSecond(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		drift float64
	}{
		{name: "steady", drift: 0},
		{name: "slow device", drift: 1e-4},
		{name: "fast device", drift: -1e-4},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			dev := &device{mix: &mixer{}, start: start, drift: tc.drift}

			var last int64

			for second := 1; second <= 600; second++ {
				// The timer fires a few milliseconds after the second.
				at := start.Add(time.Duration(second) * time.Second)
				dev.until(at.Add(3 * time.Millisecond))

				got := dev.mix.frame(at)
				if got < dev.mix.pos {
					t.Fatalf("second %d placed at frame %d, handed over already to %d", second, got, dev.mix.pos)
				}

				// A beat may stray from the device's second by a millisecond.
				want := rate * (1 - tc.drift)
				if second > 1 && math.Abs(float64(got-last)-want) > rate/1000 {
					t.Fatalf("second %d sounds %d frames after the last, want %.0f", second, got-last, want)
				}

				last = got
			}
		})
	}
}

func TestFrameRebearsAfterStall(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mix := &mixer{}
	now := (&device{mix: mix, start: start}).until(start.Add(3 * time.Second))

	// The device stops for five seconds, then reads again.
	now = now.Add(5 * time.Second)
	mix.pos += playerFrames
	mix.observe(now)

	if got, want := mix.frame(now), mix.pos+int64(aim*rate); got != want {
		t.Fatalf("after a stall the present is frame %d, want %d", got, want)
	}
}

func TestFrameBeforeRead(t *testing.T) {
	t.Parallel()

	mix := &mixer{pos: 7}
	if got := mix.frame(time.Now()); got != 7 {
		t.Fatalf("unread mixer placed now at frame %d, want 7", got)
	}
}

func TestTick(t *testing.T) {
	t.Parallel()

	snd, err := loadSounds()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		second int
		minute int
		quiet  bool
		voices int
	}{
		{name: "tic", second: 2, minute: 7, voices: 1},
		{name: "tok", second: 3, minute: 7, voices: 1},
		{name: "quiet", second: 3, minute: 7, quiet: true, voices: 0},
		{name: "hour", second: 0, minute: 0, voices: 1 + 16 + 3},
		{name: "quiet quarter", second: 0, minute: 15, quiet: true, voices: 4},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mix := &mixer{}
			clock := &striker{}
			clock.strike(at(14, 59, 58))

			tick(mix, &snd, clock, at(15, tc.minute, tc.second), tc.quiet)

			if len(mix.voices) != tc.voices {
				t.Fatalf("scheduled %d voices, want %d", len(mix.voices), tc.voices)
			}
		})
	}
}

func TestPerformCutsShort(t *testing.T) {
	t.Parallel()

	snd, err := loadSounds()
	if err != nil {
		t.Fatal(err)
	}

	mix := &mixer{}
	mix.schedule(0, snd.tic)
	perform(mix, &snd, 0, 4, 12)
	perform(mix, &snd, 0, 1, 0)

	if want := 1 + 4; len(mix.voices) != want {
		t.Fatalf("%d voices, want %d", len(mix.voices), want)
	}

	// The cuckoo waits for the hour's chime to ring out and the doors to open.
	perform(mix, &snd, 0, 4, 1)

	_, total := quarter(4)
	call := mix.voices[len(mix.voices)-1]

	if want := int64(math.Round((total + doorSeconds + callLead) * rate)); call.start != want {
		t.Fatalf("cuckoo starts at frame %d, want %d", call.start, want)
	}
}

func TestPerformHoldsTogetherWhileRead(t *testing.T) {
	t.Parallel()

	snd, err := loadSounds()
	if err != nil {
		t.Fatal(err)
	}

	// The mixer is read one frame at a time throughout, so its clock moves
	// while the strike is being laid out.
	mix := &mixer{}
	done := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		buf := make([]byte, bytesPerFrame)

		for {
			select {
			case <-done:
				return
			default:
				_, _ = mix.Read(buf)
			}
		}
	}()

	perform(mix, &snd, 0, 4, 12)
	close(done)
	<-stopped

	// Every note must place the strike's start on the same frame.
	notes, _ := quarter(4)

	var origin int64

	for i, n := range notes {
		got := mix.voices[i].start - int64(math.Round(n.at*rate))
		if i == 0 {
			origin = got

			continue
		}

		if got != origin {
			t.Fatalf("note %d puts the strike at frame %d, note 0 at %d", i, got, origin)
		}
	}
}

// TestListen plays the hour through the speakers. It is the only test that
// opens a sound device, so it runs only when asked:
//
//	CUCKOO_LISTEN=1 go test -run Listen
func TestListen(t *testing.T) {
	t.Parallel()

	if os.Getenv("CUCKOO_LISTEN") == "" {
		t.Skip("set CUCKOO_LISTEN=1 to hear the hour")
	}

	snd, err := loadSounds()
	if err != nil {
		t.Fatal(err)
	}

	mix := &mixer{}

	_, player, err := openPlayer(mix)
	if err != nil {
		t.Fatal(err)
	}

	// Give oto a moment to read, so the beats are placed by the wall clock.
	time.Sleep(time.Second / 4)

	start := time.Now()

	for beat := range 4 {
		sound := snd.tic
		if beat%2 == 1 {
			sound = snd.tok
		}

		mix.schedule(mix.frame(start.Add(time.Duration(beat)*time.Second)), sound)
	}

	perform(mix, &snd, mix.frame(start), 4, 3)

	_, total := quarter(4)
	time.Sleep(time.Duration((total + doorSeconds + 3*(callSeconds+callGap) + 1) * float64(time.Second)))
	runtime.KeepAlive(player)
}
