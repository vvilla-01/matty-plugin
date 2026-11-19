import type * as Real from '../../src/client';
import type * as RealTwitch from '../../src/twitchEmbed';
import type * as RealEmojiImages from '../../src/emojiImages';
import type * as RealSoundEncode from '../../src/soundEncode';

import * as Mock from './client';
import * as MockTwitch from './twitchEmbed';
import * as MockEmojiImages from './emojiImages';
import * as MockSoundEncode from './soundEncode';

// The mock client must have EVERY export the real one has.
//
// webpack swaps this module in for src/client wholesale, so a missing export is
// an undefined at runtime rather than an error at build time — the lab loads,
// renders half a panel, and the console names a function nobody added. That has
// now happened twice.
//
// Keys, not values. tsc already compiles every component against the REAL
// client's types, so a signature that drifts is caught at the call site; what
// it cannot see is that the stand-in has all the same names. And comparing
// values would fail on nothing more interesting than a URL literal that differs
// between the two by design.
//
// This assignment is the whole point of the file: it does not compile unless
// every name is present. Importing it from the lab's entry point is what makes
// tsc look at it.
type EveryExport = Record<keyof typeof Real, unknown>;

export const clientSurfaceMatches: EveryExport = Mock;

// The same for Twitch's player and windows (design/twitch-live.md 11.3),
// which the lab swaps too.
type EveryTwitchExport = Record<keyof typeof RealTwitch, unknown>;

export const twitchEmbedSurfaceMatches: EveryTwitchExport = MockTwitch;

// And for the soundboard's emoji pictures and its encoder (design/3.2.0.md
// 7), swapped since 3.2.0.
type EveryEmojiImagesExport = Record<keyof typeof RealEmojiImages, unknown>;

export const emojiImagesSurfaceMatches: EveryEmojiImagesExport = MockEmojiImages;

type EverySoundEncodeExport = Record<keyof typeof RealSoundEncode, unknown>;

export const soundEncodeSurfaceMatches: EverySoundEncodeExport = MockSoundEncode;
