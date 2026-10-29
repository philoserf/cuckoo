// Command cuckoo keeps the local time by ear: a pendulum's tic-tok each
// second, the Westminster quarters on the quarter hour, and on the hour the
// four quarters followed by a cuckoo for each hour on a twelve-hour dial.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/ebitengine/oto/v3"
	"github.com/hajimehoshi/go-mp3"
)

const (
	rate          = 44100
	bytesPerFrame = 8    // two float32 channels
	playerFrames  = 2048 // about 46 ms read ahead of what is heard
	silence       = 0.0001

	groupTick   = 0
	groupStrike = 1

	noteSeconds   = 0.72
	phraseSeconds = 3.52
	chimeLead     = 0.05
	ring          = 2.6
	doorSeconds   = 0.72

	callSeconds = 1.05
	callGap     = 0.18
	callLead    = 0.04
)

//go:embed cuckoo.mp3
var cuckooMP3 []byte

var errSampleRate = errors.New("cuckoo.mp3 is not 44.1 kHz")

func main() {
	quiet := flag.Bool("quiet", false, "mute the pendulum's tic-tok")

	flag.Parse()

	err := run(*quiet)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cuckoo:", err)
		os.Exit(1)
	}
}

func run(quiet bool) error {
	snd, err := loadSounds()
	if err != nil {
		return err
	}

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
}

// tick is one second of the clock: a beat of the pendulum, then a strike if
// this second opens a quarter.
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

// striker decides what each second strikes. The first reading only takes
// its bearings, so a quarter already underway at startup is not replayed,
// and a boundary is struck once however often it is read.
type striker struct {
	held   string
	opened bool
}

func (s *striker) strike(at time.Time) (int, int) {
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
}

// perform schedules a strike, cutting short any strike still sounding. The
// cuckoo waits for the chime to ring out and the doors to open.
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

// note is one strike of a chime rod, at seconds from the start of a quarter.
type note struct {
	at  float64
	rod int
}

// quarter lays out the Westminster phrases for a quarter: when each note
// sounds, which rod it strikes, and when the last note has rung out.
func quarter(count int) ([]note, float64) {
	const gSharp4, fSharp4, e4, b3 = 0, 1, 2, 3

	phrases := [][]int{
		{gSharp4, fSharp4, e4, b3},
		{e4, gSharp4, fSharp4, b3},
		{e4, fSharp4, gSharp4, e4},
		{gSharp4, e4, fSharp4, b3},
		{b3, fSharp4, gSharp4, e4},
	}
	quarters := [][]int{{0}, {1, 2}, {3, 4, 0}, {1, 2, 3, 4}}

	if count <= 0 {
		return nil, 0
	}

	var (
		notes []note
		last  float64
	)

	for p, phrase := range quarters[min(count, len(quarters))-1] {
		for n, rod := range phrases[phrase] {
			last = float64(p)*phraseSeconds + float64(n)*noteSeconds
			notes = append(notes, note{at: chimeLead + last, rod: rod})
		}
	}

	return notes, chimeLead + last + ring
}

// sounds holds every sound the clock makes, rendered once at startup as
// mono samples at the mixer's rate.
type sounds struct {
	tic, tok, call []float32
	rods           [4][]float32
}

func loadSounds() (sounds, error) {
	call, err := decodeCall(cuckooMP3)
	if err != nil {
		return sounds{}, err
	}

	return sounds{
		tic:  click(),
		tok:  knock(),
		call: call,
		rods: [4][]float32{rod(415.3), rod(369.99), rod(329.63), rod(246.94)},
	}, nil
}

// expRamp is Web Audio's exponentialRampToValueAtTime: it starts at from,
// reaches to at t=span, moves geometrically in between, and holds at either
// end.
func expRamp(from, to, t, span float64) float64 {
	if t <= 0 {
		return from
	}

	if t >= span {
		return to
	}

	return from * math.Pow(to/from, t/span)
}

// envelope rises from silence to peak over attack, then falls back to
// silence by release.
func envelope(t, peak, attack, release float64) float64 {
	if t < attack {
		return expRamp(silence, peak, t, attack)
	}

	return expRamp(peak, silence, t-attack, release-attack)
}

func render(seconds float64, sample func(t float64, i int) float64) []float32 {
	out := make([]float32, max(1, int(rate*seconds)))
	for i := range out {
		out[i] = float32(sample(float64(i)/rate, i))
	}

	return out
}

// click is the tic: a short, bright ping.
func click() []float32 {
	return render(0.022, func(t float64, _ int) float64 {
		tone := math.Sin(2*math.Pi*2140*t) * math.Exp(-t/0.0035) * 0.45

		return tone * envelope(t, 0.16, 0.003, 0.022)
	})
}

// knock is the tok: lower and woodier than the tic.
func knock() []float32 {
	return render(0.03, func(t float64, _ int) float64 {
		tone := math.Sin(2*math.Pi*620*t)*0.7 + math.Sin(2*math.Pi*310*t)*0.3

		return tone * math.Exp(-t/0.006) * 0.5 * envelope(t, 0.18, 0.004, 0.03)
	})
}

// rod is one struck chime rod: a hammer click, then partials that die away,
// the lowest one slowest.
func rod(frequency float64) []float32 {
	partials := []struct{ ratio, amp, decay float64 }{
		{1, 0.35, 2.6},
		{2.003, 0.21, 1.5},
		{2.76, 0.079, 0.3},
		{2.99, 0.053, 0.45},
		{4.07, 0.088, 0.07},
	}

	return render(ring+0.02, func(t float64, i int) float64 {
		sum := 0.0

		for _, p := range partials {
			if t >= p.decay+0.02 {
				continue
			}

			attack := min(0.004, p.decay/3)
			sum += math.Sin(2*math.Pi*frequency*p.ratio*t) * envelope(t, p.amp, attack, p.decay)
		}

		if t < 0.016 {
			hashed := math.Sin(float64(i)*12.9898) * 43758.5453
			noise := (hashed-math.Floor(hashed))*2 - 1
			sum += noise * math.Exp(-t/0.0022) * envelope(t, 0.28, 0.001, 0.016)
		}

		return sum
	})
}

// decodeCall decodes the cuckoo's call to mono and shapes it the way the
// clock plays it: trimmed to one call, faded in, held, and faded out.
func decodeCall(data []byte) ([]float32, error) {
	decoder, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode cuckoo.mp3: %w", err)
	}

	if decoder.SampleRate() != rate {
		return nil, errSampleRate
	}

	pcm, err := io.ReadAll(decoder)
	if err != nil {
		return nil, fmt.Errorf("decode cuckoo.mp3: %w", err)
	}

	// go-mp3 always decodes to interleaved stereo, signed 16-bit little-endian.
	stereo := make([]int16, len(pcm)/2)

	err = binary.Read(bytes.NewReader(pcm), binary.LittleEndian, stereo)
	if err != nil {
		return nil, fmt.Errorf("decode cuckoo.mp3: %w", err)
	}

	frames := min(len(stereo)/2, int(callSeconds*rate))
	duration := float64(frames) / rate
	release := max(0.06, duration-0.12)
	out := make([]float32, frames)

	for i := range out {
		mono := (float64(stereo[2*i]) + float64(stereo[2*i+1])) / 2 / math.MaxInt16

		t := float64(i) / rate

		gain := 0.62
		if t < 0.02 {
			gain = expRamp(silence, 0.62, t, 0.02)
		} else if t >= release {
			gain = expRamp(0.62, silence, t-release, duration-release)
		}

		out[i] = float32(mono * gain)
	}

	return out, nil
}

// cue is a sound to schedule, at seconds from the mixer's clock.
type cue struct {
	samples []float32
	at      float64
}

type voice struct {
	samples []float32
	start   int64
	group   int
}

// mixer is the clock's sound card: it adds up whatever is scheduled and
// hands oto interleaved stereo float32. Time is the count of frames it has
// handed over, so sounds placed on it keep exact time relative to each other.
type mixer struct {
	voices []voice
	mix    []float32
	pos    int64
	mu     sync.Mutex
}

func (m *mixer) Read(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	frames := len(p) / bytesPerFrame
	if cap(m.mix) < frames {
		m.mix = make([]float32, frames)
	}

	mix := m.mix[:frames]
	clear(mix)

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

	for i, s := range mix {
		bits := math.Float32bits(max(-1, min(1, s)))
		binary.LittleEndian.PutUint32(p[i*bytesPerFrame:], bits)
		binary.LittleEndian.PutUint32(p[i*bytesPerFrame+4:], bits)
	}

	return frames * bytesPerFrame, nil
}

func (m *mixer) schedule(samples []float32, at float64, group int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.add(cue{samples: samples, at: at}, group)
}

// replace cuts short everything sounding in group and schedules cues in its
// place. It holds the lock throughout, so every cue is placed against the
// same frame and no Read can land between them.
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

// add places a cue at seconds from the mixer's clock. The caller holds the
// lock.
func (m *mixer) add(c cue, group int) {
	start := m.pos + int64(math.Round(c.at*rate))
	m.voices = append(m.voices, voice{samples: c.samples, start: start, group: group})
}
