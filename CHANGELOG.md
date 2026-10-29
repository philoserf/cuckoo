# Changelog

## 0.1.1

### Fixed

- **Tic-tok keeps time:** each beat is placed on the frame that answers to its wall-clock second, so beats sound a second apart. Before, a beat went wherever oto's last read had left the mixer, so successive beats strayed by up to 46 ms.

## 0.1.0

The first release: a cuckoo clock for the terminal, heard and not seen.

### Added

- **Tic-tok:** a pendulum's tic on even seconds and tok on odd ones, following the wall-clock second. `--quiet` mutes it and leaves the strikes.
- **Westminster quarters:** one phrase at a quarter past, two at half past and three at a quarter to, on four synthesized chime rods tuned to G♯4, F♯4, E4 and B3.
- **The hour:** all four phrases, then the cuckoo calls once for each hour on a twelve-hour dial. Midnight and noon both get twelve calls.
- **No replay at startup:** a quarter already underway when the clock starts is not played.
- **Audio failures end the run:** if the audio device fails, cuckoo prints the error and exits 1 instead of keeping time in silence.
- **`task listen`** plays the hour straight away, without waiting for one.
