// How loud a sound is, and making every sound on the board about as loud as
// every other (design/3.2.0.md 7.2 step 1.3), before the browser encodes it.
//
// ITU-R BS.1770-4's integrated loudness, for one channel at 48 kHz: the
// K-weighting (a high shelf and a high-pass, with the standard's own
// coefficients for 48 kHz), mean squares over 400 ms blocks overlapping by
// 75 %, the absolute gate at −70 LUFS and the relative gate 10 LU under the
// blocks that pass it. A sound shorter than 400 ms is measured as one block
// padded with silence. Then a gain to −20 LUFS, and a 5 ms look-ahead
// sample-peak limiter that holds it under −1 dBFS. Not DynamicsCompressorNode,
// whose behaviour differs between engines.
//
// Only the browser runs BS.1770: the server holds a cruder ceiling (the same
// two filters, ungated) to every upload, with margins this side never reaches.
//
// Imports nothing, so build/check-sounds.mjs can hold it to answers known in
// advance.

export const RATE = 48000;
export const TARGET_LUFS = -20;
export const CEILING = 10 ** (-1 / 20);

const BLOCK = RATE * 0.4;
const STEP = RATE * 0.1;
const LOOKAHEAD = RATE * 0.005;

// The release after the limiter has pulled a peak down: back to no reduction
// over 50 ms, so it never pumps on a single click.
const RELEASE = 1 / (RATE * 0.05);

type Biquad = {b0: number; b1: number; b2: number; a1: number; a2: number};

// BS.1770-4, Table 1 and Table 2, at 48 kHz.
const SHELF: Biquad = {b0: 1.53512485958697, b1: -2.69169618940638, b2: 1.19839281085285, a1: -1.69065929318241, a2: 0.73248077421585};
const HIGH_PASS: Biquad = {b0: 1.0, b1: -2.0, b2: 1.0, a1: -1.99004745483398, a2: 0.99007225036621};

function filter(input: Float32Array, q: Biquad): Float32Array {
    const out = new Float32Array(input.length);
    let x1 = 0;
    let x2 = 0;
    let y1 = 0;
    let y2 = 0;
    for (let i = 0; i < input.length; i += 1) {
        const x = input[i];
        const y = (q.b0 * x) + (q.b1 * x1) + (q.b2 * x2) - (q.a1 * y1) - (q.a2 * y2);
        out[i] = y;
        x2 = x1;
        x1 = x;
        y2 = y1;
        y1 = y;
    }

    return out;
}

export function kWeighted(samples: Float32Array): Float32Array {
    return filter(filter(samples, SHELF), HIGH_PASS);
}

const loudnessOf = (meanSquare: number): number => (meanSquare > 0 ? -0.691 + (10 * Math.log10(meanSquare)) : -Infinity);

// The mean square of each 400 ms block, stepping by 100 ms. A sound shorter
// than a block is one block, padded with silence.
function blockPowers(weighted: Float32Array): number[] {
    const powers: number[] = [];
    if (weighted.length < BLOCK) {
        let sum = 0;
        for (let i = 0; i < weighted.length; i += 1) {
            sum += weighted[i] * weighted[i];
        }
        powers.push(sum / BLOCK);

        return powers;
    }

    for (let start = 0; start + BLOCK <= weighted.length; start += STEP) {
        let sum = 0;
        for (let i = start; i < start + BLOCK; i += 1) {
            sum += weighted[i] * weighted[i];
        }
        powers.push(sum / BLOCK);
    }

    return powers;
}

// Integrated loudness in LUFS, or -Infinity for a sound with nothing above the
// absolute gate: silence, as far as levelling it goes.
export function integratedLoudness(samples: Float32Array): number {
    const powers = blockPowers(kWeighted(samples));
    const audible = powers.filter((power) => loudnessOf(power) > -70);
    if (audible.length === 0) {
        return -Infinity;
    }

    const mean = (list: number[]) => list.reduce((sum, power) => sum + power, 0) / list.length;
    const relative = loudnessOf(mean(audible)) - 10;
    const counted = audible.filter((power) => loudnessOf(power) > relative);

    return loudnessOf(mean(counted));
}

// The highest absolute sample.
export function peakOf(samples: Float32Array): number {
    let peak = 0;
    for (let i = 0; i < samples.length; i += 1) {
        peak = Math.max(peak, Math.abs(samples[i]));
    }

    return peak;
}

// Never above the ceiling, by gain alone: each sample's gain reaches what its
// loudest neighbour in the next 5 ms needs in a straight line, so a peak is
// met already turned down rather than clipped, and comes back up over 50 ms.
// Every sample's gain is at most what its own value needs, so nothing passes
// the ceiling.
export function limit(samples: Float32Array, ceiling = CEILING): Float32Array {
    const length = samples.length;
    const needs = new Float32Array(length);
    let any = false;
    for (let i = 0; i < length; i += 1) {
        const level = Math.abs(samples[i]);
        needs[i] = level > ceiling ? ceiling / level : 1;
        any = any || needs[i] < 1;
    }
    if (!any) {
        return samples.slice();
    }

    // Where the next sample that needs turning down is, so a stretch with
    // none within 5 ms costs nothing to look ahead over.
    const next = new Int32Array(length + 1);
    next[length] = length;
    for (let i = length - 1; i >= 0; i -= 1) {
        next[i] = needs[i] < 1 ? i : next[i + 1];
    }

    const out = new Float32Array(length);
    let gain = 1;
    for (let i = 0; i < length; i += 1) {
        let wanted = 1;
        for (let ahead = next[i] - i; ahead <= LOOKAHEAD && i + ahead < length; ahead += 1) {
            const need = needs[i + ahead];
            if (need < 1) {
                const ramp = need + ((1 - need) * (ahead / LOOKAHEAD));
                if (ramp < wanted) {
                    wanted = ramp;
                }
            }
        }

        gain = Math.min(wanted, gain + RELEASE);
        out[i] = samples[i] * gain;
    }

    return out;
}

// A sound made ready for the board: the stretch kept, with 5 ms fades at both
// ends so it starts and stops without a click, brought to −20 LUFS, and held
// under −1 dBFS. loudness is what it measured before; -Infinity means silence,
// which the add flow refuses ("That sounds silent.").
export function levelled(samples: Float32Array): {samples: Float32Array; loudness: number} {
    const faded = samples.slice();
    const fade = Math.min(Math.round(RATE * 0.005), Math.floor(faded.length / 2));
    for (let i = 0; i < fade; i += 1) {
        const shape = i / fade;
        faded[i] *= shape;
        faded[faded.length - 1 - i] *= shape;
    }

    const loudness = integratedLoudness(faded);
    if (!Number.isFinite(loudness)) {
        return {samples: faded, loudness};
    }

    const gain = 10 ** ((TARGET_LUFS - loudness) / 20);
    const raised = new Float32Array(faded.length);
    for (let i = 0; i < faded.length; i += 1) {
        raised[i] = faded[i] * gain;
    }

    return {samples: limit(raised), loudness};
}
