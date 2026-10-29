# cuckoo

![Status: Stable](https://img.shields.io/badge/Status-Stable-brightgreen.svg)

A cuckoo clock for the terminal, heard and not seen. It keeps the computer's local time and prints nothing, unless the audio device fails: then it says so and exits.

```sh
go install github.com/philoserf/cuckoo@latest
cuckoo           # Ctrl-C to stop
cuckoo --quiet   # strikes only, no tic-tok
```

The pendulum tics and toks every second. On the quarter hour it plays the Westminster quarters: one phrase at a quarter past, two at half past, three at a quarter to. On the hour it plays all four, then the cuckoo calls once for each hour on a twelve-hour dial. A quarter already underway when the clock starts is not replayed.

`task listen` plays the hour straight away, so you can hear it without waiting.

## Sounds

The tic, the tok and the chime rods are synthesized. Each rod is a hammer click followed by partials that die away, the lowest one slowest, tuned to the usual Big Ben pitches: G♯4, F♯4, E4 and B3. Notes inside a phrase are 0.72 seconds apart, and each phrase starts 3.52 seconds after the one before.

The cuckoo's call is `cuckoo.mp3`, from [SoundBible](https://soundbible.com/1261-Cuckoo-Clock.html): public domain, by Mike Koenig, taken from a public-domain movie and reworked.

## Inspiration

cuckoo is inspired by Robert C. Martin's [cuckoo-clock](https://github.com/unclebob/cuckoo-clock), an analog cuckoo clock for the browser. cuckoo's sounds, their timings and the strike rules all keep that clock's numbers. One change: the tic-tok follows the wall-clock second instead of a pendulum's own swing.

## Development

`task setup` installs the toolchain, and bare `task` runs the gate.
