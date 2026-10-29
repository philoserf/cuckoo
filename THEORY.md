# A theory of cuckoo

This is the understanding you need to change cuckoo without breaking what it is. It is not a tour of `main.go`. It covers what the program is trying to be, the ideas the code rests on, where those ideas are thin, and which of these claims to trust.

## What it is for

cuckoo is a clock you cannot see. It answers one question for someone in the room: roughly what time is it, without looking? It works at three scales, with one sound each:

- **Second:** a pendulum's tic on even seconds and tok on odd ones.
- **Quarter hour:** the Westminster quarters, one, two or three four-note phrases.
- **Hour:** all four phrases, then the cuckoo calls the hour on a twelve-hour dial. Midnight and noon both get twelve calls.

The only option is `--quiet`. It mutes the pendulum but never the strike, because the strike carries the information and the tic-tok is atmosphere.

It is also a port. Every sound, every timing and every strike rule comes from a browser cuckoo clock written in TypeScript against Web Audio, and the Go code keeps that clock's numbers. Much of what looks arbitrary here is that design carried across from a push-model audio graph, where you tell the browser when to play, into a pull-model device, where the device asks for samples. The places where the two models differ are where this program has needed the most care.

## Two clocks

The organizing idea is that the program runs on two clocks, and the mixer maps one onto the other.

**The wall clock decides what happens.** `run` wakes once per wall second and hands `tick` the second it aimed at, not the timer's firing time. Each pass aims fresh at the next whole second from `time.Now()` rather than using a `time.Ticker`, and builds that second from `now` so it keeps the monotonic reading. NTP slew, a late timer or a wake from sleep is therefore corrected at the next tick instead of accumulating. `tick` picks tic or tok from the parity of that second and asks `striker` whether the reading strikes.

**The mixer's frame count decides when a sound plays.** `mixer.pos` counts the frames handed to oto so far. Every sound is placed at some number of seconds after an origin frame, never before `mixer.pos`. A strike is laid out in seconds from its own start: `quarter` returns note times from zero, and `perform` adds the door pause and the calls. Its internal timing is arithmetic on frames, not a run of timers, and that arithmetic keeps the tune in time.

**Where the two meet,** `mixer.frame` places a wall-clock instant on a frame. oto reads in bursts, so `mixer.pos` jumps 2048 frames at a time and is only a rough guide to the wall clock. Anchoring each beat at `mixer.pos`, as the clock first did, put successive beats up to a read apart from a second, and the tic-tok was audibly uneven. Each `Read` now takes a bearing: how far the frames handed over run ahead of the wall time elapsed since the first read. The furthest bearing holds steady across the bursts. It eases back a frame per read, so it follows the device's clock as that drifts from the wall clock's. A read more than `stall` behind it means the device stopped, and that read becomes the bearing. `frame` adds `aim` (20 ms), room for the timer to fire late, so a beat placed on time is never on a frame already handed over. If one is, the beat sounds at once. `TestFrameKeepsTheSecond` runs a simulated device with late reads and drift to hold this.

Two consequences follow, and both have already cost a fix:

- **A group of sounds is exact only if it is placed in one step.** `mixer.replace` cuts short the sounding strike and places every cue of the new one while holding the lock once. That puts the whole strike against a single origin. When `perform` scheduled note by note, a read could land between two notes and shift the rest of the strike late (fixed in #3; `TestPerformHoldsTogetherWhileRead` reads the mixer throughout a strike to hold this). The beat and the strike in the same `tick` are placed in two steps, but from the same origin, so a read between them moves neither. Anything that must keep exact time with something else has to go through one locked step.
- **`mixer.pos` is a read-ahead position, not the audible one.** It runs ahead of the speaker by whatever oto has buffered. With oto's defaults that was 0.5–1.0 s, and every sound landed on a half-second grid (measured, and fixed in #2 with `playerFrames`). oto now reads 2048 frames, about 46 ms, at a time; the reads were measured on average 46.5 ms apart. Together with oto's player buffer and the macOS AudioQueue (four buffers of 1536 frames), the lead should be roughly 0.1–0.23 s. That range comes from reading oto's source, not from a measurement at the speaker. The comments on `playerFrames` and in `openPlayer` say so.

The small leads, `chimeLead` (0.05 s) and `callLead` (0.04 s), were safety margins in Web Audio, whose `currentTime` advances 128 frames at a time. Against a 46 ms read they are again about one step, which is roughly what they were written for.

## What the striker decides

`striker` turns one reading of local wall time into a number of chimes and a number of cuckoos.

- **A boundary** is second 0 of minute 0, 15, 30 or 45, read with `Clock()` in local time. Zones offset by half or three-quarter hours therefore strike on their own quarters. Across a fall-back change the repeated hour is struck through twice. The skipped spring-forward hour is never struck.
- **The first reading only takes its bearings.** Starting at 10:15:00.4 doesn't play the quarter. If that first reading is itself on a boundary, it is held.
- **`held` remembers** the boundary last struck or held, as minutes since midnight (`-1` for none), and any reading off a boundary clears it. So a boundary is struck once per unbroken run of readings inside its second. If the wall clock steps back across a boundary after an off-boundary reading, that boundary strikes again, and `replace` cuts the first strike short. `TestStrike` is one striker read in order, and the order of its table is the test.
- **Missed boundaries are never caught up.** If no reading lands inside a boundary second, for example because the machine slept, that quarter passes in silence. This matches the bearings rule: the clock never plays anything late.

The striker is written so it doesn't care how often it is read, which is why it carries `held` at all. In Go, two readings in the same second happen only if the wall clock steps backward. It does care how rarely it is read, though: it needs at least one reading inside every boundary second. The loop's aim-fresh-each-second shape is what guarantees that, so half of the striker's contract lives in `run`.

## What a strike is

`quarter` is the Westminster chime as data, for the 1–4 quarters the striker reports. It has five phrases (the "changes") over four rods, in the standard sequence: phrase 1; phrases 2 and 3; phrases 4, 5 and 1; phrases 2, 3, 4 and 5.

- **Spacing:** notes in a phrase are 0.72 s apart and phrases start 3.52 s apart, so the last note of each phrase is held 1.36 s. That held note is what makes the tune recognizable.
- **End of the chime:** `quarter` reports the chime as finished `ring` (2.6 s) after the last note.
- **Door pause:** `perform` waits a further `doorSeconds`. That is the doors opening, on a clock that has no doors to see.
- **Calls:** `perform` then places the calls one after another, spaced by the decoded call's actual length plus `callGap`.

The noon strike runs about 31 s.

The rods are named by pitch: the constants `gSharp4`, `fSharp4`, `e4` and `b3` are rod indices. `quarter`, `TestQuarter` and the tuning table in `loadSounds` all use those names, and the table is keyed by them, so the name-to-pitch link is written once (#4). The arrays are sized `[4]`, so tuning a fifth rod in that table won't compile until they grow.

Every voice carries a `strike` flag, and the only thing done with it is `replace` in `perform`, which cuts short the strike still sounding before placing the new one. Strikes are 15 minutes apart, so the cut-short happens in practice only when the wall clock moves back over a boundary, and in tests. The flag exists for this one rule. A second kind of sound that must be cut short separately would be a design change.

## Sounds: rendered once, in Web Audio's terms

Every sound is rendered once at startup into mono float32 at 44.1 kHz, and `mixer.Read` only adds. That split is load-bearing. `Read` runs on oto's goroutine about every 46 ms with the mixer's lock held. Work that synthesizes or allocates there stalls playback and blocks scheduling.

Each synthesized sound transcribes a Web Audio graph node for node, and the code reads most easily that way: a source multiplied by a gain envelope.

- **The source** is a sine or a sum of sines, sometimes with a `math.Exp` decay built in. In the original that was presumably a generated buffer.
- **The gain** is `envelope`, built on `expRamp`, which reproduces `exponentialRampToValueAtTime` exactly: it starts at the first value, approaches the second geometrically, and holds at either end.

That is why `click` and `knock` each decay twice. One decay belongs to the buffer and one to the gain node, and folding them together changes the sound. Each rod partial is cut at its decay plus 0.02 s, which is an oscillator's stop time. The fundamental decays over `ring` itself, so the rod's render length, `ring + 0.02`, follows from the table rather than restating it.

The hammer noise is a deterministic hash of the sample index, the shader idiom `fract(sin(x)·43758.5453)`, rather than random. So every rod has the same click and every render is reproducible, which lets `TestSounds` pin exact frame counts. Those counts are how you know a refactor didn't change a sound.

The cuckoo is the only recorded sound. `cuckoo.mp3` is embedded and about 2.06 s long. `decodeCall` keeps the first 1.05 s, which its comment calls one call. It mixes the call to mono and shapes it with the same ramp semantics: a 20 ms fade-in, a hold at 0.62, and a fade over the last 0.12 s. It rejects any rate but 44.1 kHz with `errSampleRate`, because nothing resamples. The render rate, the mp3's rate and oto's rate are one constant, `rate`, and that is a hard constraint of the design.

## The mixer's contract

`mixer` is an `io.Reader` that never ends:

- **It always fills the buffer.** Every `Read` fills the whole buffer, with silence when nothing is scheduled, and never returns `io.EOF`. oto pauses a player whose source reaches EOF and doesn't resume it, so an "idle means EOF" change would stop the clock for good.
- **It sums and hard-clips.** It adds the voices, clips the sum to [-1, 1] with no master gain, and writes the mono sum to both channels. No sound the clock makes reaches the clip. Summed without clipping, the loudest strike (noon, with tic-tok under it) peaks at 0.625.
- **It drops finished voices.** A voice is removed once its last frame has been handed over.

`schedule` places one beat. `replace` swaps the whole strike in one step. Both go through `add`, which assumes the caller holds the lock. Zero values mean something: a fresh `mixer{}` or `striker{}` is ready to use, which is why the config disables `exhaustruct`.

## The seams

**oto** is the boundary that matters most. Three facts about it are load-bearing, and the code now honors all three:

- **oto closes a player once its wrapper is garbage collected.** `runtime.KeepAlive(player)` is not decoration. Without it, the first collection after startup closed the player 1.6 s in, and the clock ran on in silence (fixed in #1).
- **oto reads its source in chunks the size of the player's buffer.** `playerFrames` sets that size and with it how far `mixer.pos` jumps per read (#2). `mixer.frame` places sounds between the jumps.
- **oto's errors are fatal.** A context or player whose `Err` is non-nil never plays again. `run` checks both on every tick and returns the error, so the clock exits with a message rather than keeping time in silence (#16).

`openPlayer` opens the device for both `run` and `TestListen` (#17), so `task listen` plays exactly what the clock plays. The two used to carry separate copies, and those had diverged: the test held its player alive while `run` didn't, which is why `task listen` sounded fine while the clock went silent. The player's lifetime still belongs to each caller, because it must outlive the caller's loop or sleep, not `openPlayer`.

**The test boundary** follows the device boundary exactly. Everything below the device is pure and fully covered: striker sequences, quarter layouts, sound lengths and peaks, mixer arithmetic across reads and under a concurrent reader, `tick`'s voice counts, and `perform`'s cut-short and cuckoo entrance. `main`, `run` and `openPlayer` are untested. Their statements are the whole count the coverage ratchet holds, so every change to them has moved the ratchet. `task listen` is the only check that opens a device, and it sits outside the gate. A change to `run` is a change the gate cannot see, so listen to it.

**The TypeScript original** is the deepest seam, and it isn't in the repository. The port kept the numbers. The clock they were measured against has had to be brought back close to the original's by hand.

The split into functions is principled: `striker` decides what, `quarter` lays out, the sounds are rendered once, and `mixer` decides when. Keeping it all in one file is the workspace's convention, not part of the design.

## What it accommodates, and what it doesn't

**The design makes room for:**

- **A new sound or a different chime,** such as Whittington: a render function and a field on `sounds`, or new phrase tables and rod names. The striker doesn't need to change.
- **A policy on what to strike,** such as quiet hours or hours only. This belongs in `tick`, between `strike` and `perform`. The striker's job is only to report the boundary.
- **Volume:** `Player.SetVolume`, or one gain applied in `Read`.
- **A strike on demand,** from a signal or a key. `perform` already replaces whatever strike is sounding.

**These would require rethinking something fundamental:**

- **A second sample rate:** any recording not at 44.1 kHz needs a resampler, and nothing resamples.
- **A beat on the audible second:** beats are now a steady second apart, but each is heard a fixed lead after its second. Placing it on the second as the listener hears it means knowing the full lead and subtracting it. The lead includes the AudioQueue, which `openPlayer` doesn't configure. That needs a model of latency the mixer doesn't have.
- **Catching up missed strikes after sleep:** this contradicts the bearings rule. It would be a new rule, not a fix.

**Where a maintainer without this theory is most likely to cause damage:**

- **Swapping the timer for a `time.Ticker`:** the sub-second phase of each reading then drifts with the monotonic clock and can slide out of a boundary second.
- **Deleting `held`** because readings come once a second.
- **Scheduling related sounds in separate calls** and expecting them to stay in step.
- **Doing work in `Read`,** or letting it return EOF when idle.
- **Opening the device anywhere but `openPlayer`,** which lets `task listen` and the clock drift apart again.
- **Treating an oto error as something to retry.** oto documents them as fatal.
- **Collapsing the double decay** in `click` and `knock`.
- **Treating the leads and `doorSeconds` as dead code.** They are the original's spacing between the beat, the chime and the cuckoo.
- **Removing `runtime.KeepAlive(player)`** because the variable looks unused.

## Uncertainties

- **The original is unavailable.** I couldn't find the TypeScript clock in this workspace, the notes vault, or the philoserf GitHub repositories I searched. So "keeps its numbers" is taken on trust. Reading `doorSeconds`, the leads, `held` and the strike cut-short as carry-overs from the browser is an inference from their names and from how Web Audio works.
- **The present lead is derived, not measured.** The 0.1–0.23 s figure comes from oto's source: the player refills a full buffer whenever it falls below one, and the darwin driver queues four 1536-frame buffers. Only the read cadence was measured. I can't say whether the lag is now small enough for its purpose, because README and `CLAUDE.md` give no tolerance.
- **The small buffer hasn't been listened to.** A 2048-frame player buffer leaves less margin against an underrun if oto's goroutine is starved. I didn't hear the clock after #2, and nothing in the gate would notice crackle or a dropout.
- **The back-to-back boundary case may not be intended.** `held` separates two different boundaries read back to back. Only a wall-clock jump of at least 15 minutes landing on second 0 would produce that, and a boolean would serve one reading a second otherwise. The first Go version keyed `held` as an "hour:minute" string, which looks carried over from the browser original. I can't tell whether that case was designed for or just came along with the key.
- **The call may start late within its window.** `afinfo` reports 576 priming frames in the mp3, and go-mp3 doesn't trim them, while browsers usually do. So the Go call probably starts about 13 ms later inside its 1.05 s window than the browser's did. This is a guess and wasn't measured.
- **Sleep hasn't been tested.** Nothing tested what Go's timers and oto's queue do across a macOS sleep. The loop should resynchronize on its first tick after wake. I don't know whether the AudioQueue resumes cleanly, or whether audio queued before the sleep plays on wake.
