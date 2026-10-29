# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# cuckoo

A Go command that keeps the local time by sound alone: tic-tok each second, the Westminster quarters, and the cuckoo on the hour. It has no output and no UI.

## Commands

Run `task --list` for the current set.

**The gate is `task`.** The Go toolchain, golangci-lint, govulncheck and bun are deliberately unpinned: a red gate on code you didn't touch is the signal working, so answer the finding rather than pinning the tool. Coverage is held by a ratchet (`coverage.ratchet`, updated with `task ratchet:update`), not a percentage.

`task listen` plays the hour through the speakers. It is the only check that opens a sound device, so it stays out of the gate.

## Architecture

One file, `main.go`, in `package main`:

- `striker` decides what each second strikes. The first reading only takes its bearings, and a boundary is struck once.
- `quarter` lays out the Westminster phrases. `perform` schedules them and the cuckoo, cutting short any strike still sounding.
- Each sound is rendered once at startup to mono float32 at 44.1 kHz, the rate of the embedded `cuckoo.mp3`. `expRamp` is Web Audio's exponential ramp, which every envelope uses.
- `openPlayer` opens the sound device for both `run` and `TestListen`. oto closes a player once it is garbage collected, so each caller keeps it alive. oto's errors are fatal, so `run` checks them every second and exits on one.
- `mixer` is an `io.Reader` that oto pulls interleaved stereo from. Its clock is the number of frames it has handed over, so sounds keep exact time relative to each other. oto reads in bursts, so each read also takes a bearing on the wall clock, and `frame` places a wall-clock second on the frame that answers to it. Placing a beat at `mixer.pos` instead puts it up to a read off the second.

The sounds, the timings and the strike rules are ported from the browser cuckoo clock in TypeScript, keeping its numbers. One deliberate change: tic-tok follows the wall-clock second instead of a pendulum's own swing.
