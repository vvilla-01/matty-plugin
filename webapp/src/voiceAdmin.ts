import type {AdminVoice} from './client';

// The words of System Console > Plugins > Matty > Mumble > Talking from Matty
// (components/AdminVoice.tsx) and of the voice port's alert in the panel's
// admin alerts (components/AdminAlerts.tsx), worked out from what the server
// sends (GET /api/v1/admin/voice, and "me"'s voiceTalkAlert). It imports
// nothing but types, so build/check-voice.mjs runs it as it is written.
//
// Nothing here names anybody: the server sends how many people talk from
// Matty, never who.

// The section's title in plugin.json, where the card and the switch are.
export const VOICE_SECTION = 'Mumble';

// A line of the card, and how it is worn: the page's own colour, quieter, or
// the danger red.
export type VoiceAdminLine = {text: string; tone: 'plain' | 'quiet' | 'error'};

// The server's words as a sentence, ending in a full stop whatever they
// ended with.
function sentence(words: string): string {
    const trimmed = words.trim();

    return /[.!?…]$/.test(trimmed) ? trimmed : `${trimmed}.`;
}

// A list said as people say it: "a", "a and b", "a, b and c".
function spoken(items: string[]): string {
    if (items.length <= 1) {
        return items.join('');
    }

    return `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`;
}

// How many lines, or channels, a sentence of the card names before it
// counts the rest.
const NAMED_AT_MOST = 5;

// A list said as people say it, its first NAMED_AT_MOST items and then how
// many more there are (beyond the list's own, more): "a, b, c, d, e and 3
// more".
function spokenAtMost(items: string[], more = 0): string {
    const named = items.slice(0, NAMED_AT_MOST);
    const rest = items.length - named.length + more;

    return rest > 0 ? `${named.join(', ')} and ${rest} more` : spoken(named);
}

// The setting whose lines the card checks, as plugin.json names it.
export const VOICE_CHANNELS_SETTING = 'Channels to show';

// The card's line for the lines of Channels to show that name no channel on
// the voice server, the first five quoted as the admin wrote them and the
// rest counted; null when every line names one.
export function voiceUnmatchedLine(unmatched: string[] | undefined): VoiceAdminLine | null {
    const lines = (unmatched || []).filter((line) => line.trim() !== '');
    if (lines.length === 0) {
        return null;
    }

    const quoted = spokenAtMost(lines.map((line) => `“${line}”`));
    const verb = lines.length === 1 ? 'matches' : 'match';

    return {text: `${quoted} in ${VOICE_CHANNELS_SETTING} ${verb} no channel on the voice server.`, tone: 'error'};
}

// The card's lines for the lines of Channels to show that are not used: the
// ones longer than 256 characters, which the server sends as their first 40
// characters and "…", the first five quoted and the rest counted; and how
// many lines come after the first 200.
export function voiceLeftOutLines(tooLong: string[] | undefined, dropped: number | undefined): VoiceAdminLine[] {
    const lines: VoiceAdminLine[] = [];

    const long = (tooLong || []).filter((line) => line.trim() !== '');
    if (long.length > 0) {
        const quoted = spokenAtMost(long.map((line) => `“${line}”`));
        const said = long.length === 1 ? 'is longer than 256 characters, so it is not used' : 'are longer than 256 characters, so they are not used';
        lines.push({text: `${quoted} in ${VOICE_CHANNELS_SETTING} ${said}.`, tone: 'error'});
    }

    const rest = Math.floor(dropped || 0);
    if (rest > 0) {
        lines.push({text: `Only the first 200 lines of ${VOICE_CHANNELS_SETTING} are used; ${rest} more ${rest === 1 ? 'is' : 'are'} not.`, tone: 'error'});
    }

    return lines;
}

// The card's lines for the lines of Channels to show that name more than one
// channel (a name that several channels have), each with the channels it
// names by their paths: five such lines at most, then one that counts the
// rest.
export function voiceSeveralLines(several: AdminVoice['several']): VoiceAdminLine[] {
    const all = (several || []).filter((entry) => entry.line.trim() !== '' && entry.paths.length > 0);
    const lines: VoiceAdminLine[] = all.slice(0, NAMED_AT_MOST).map((entry) => ({
        text: `“${entry.line}” in ${VOICE_CHANNELS_SETTING} matches more than one channel: ${spokenAtMost(entry.paths, entry.more || 0)}.`,
        tone: 'quiet',
    }));

    const rest = all.length - lines.length;
    if (rest > 0) {
        lines.push({text: `${rest} more ${rest === 1 ? 'line' : 'lines'} in ${VOICE_CHANNELS_SETTING} ${rest === 1 ? 'matches' : 'match'} more than one channel.`, tone: 'quiet'});
    }

    return lines;
}

// The card's lines: the switch, the port and the addresses phones are given
// or why they are not, the voice server, who is talking from Matty now, the
// budget, the voice server's last refusal, and the lines of Channels to show
// that name no channel, are not used, or name more than one (whether talk is
// on or not: they decide what the app and the panel show). when is how the card says a time
// (time.ts postedWhen), passed in so this file needs nothing of the page.
export function voiceAdminLines(view: AdminVoice, when: (at: number) => string): VoiceAdminLine[] {
    const unmatched = voiceUnmatchedLine(view.unmatched);
    const shown = [...(unmatched ? [unmatched] : []), ...voiceLeftOutLines(view.tooLong, view.dropped), ...voiceSeveralLines(view.several)];
    if (!view.on) {
        return [{text: 'Talking from Matty is off: the app shows who is in voice, and nobody can join a channel from it.', tone: 'plain'}, ...shown];
    }

    const lines: VoiceAdminLine[] = [];
    if (view.open) {
        lines.push({text: `Phones reach this server on UDP port ${view.port}, at ${spoken(view.addresses)}.`, tone: 'plain'});
    } else if (view.problem) {
        lines.push({text: `Nobody can join from Matty: ${sentence(view.problem)}`, tone: 'error'});
    } else {
        lines.push({text: `The voice port ${view.port} is not open yet. Refresh in a moment.`, tone: 'quiet'});
    }

    if (view.connected) {
        lines.push({text: 'The voice server is connected.', tone: 'quiet'});
    } else {
        lines.push({text: 'The voice server is not connected, so nobody can join from Matty until it is.', tone: 'error'});
    }

    if (view.talking <= 0) {
        lines.push({text: 'Nobody is talking from Matty through this server now.', tone: 'plain'});
    } else {
        lines.push({text: `${view.talking} ${view.talking === 1 ? 'person is' : 'people are'} talking from Matty through this server now.`, tone: 'plain'});
    }

    lines.push(view.noBudget ? {text: 'The voice server’s autoban is marked as off, so Matty connects to it as often as people join.', tone: 'quiet'} : {text: 'Matty opens at most 8 connections to the voice server in any two minutes, below the autoban of a Mumble server as it comes.', tone: 'quiet'});

    if (view.refused) {
        const at = view.refusedAt ? ` ${when(view.refusedAt)}` : '';
        lines.push({text: `The voice server last turned somebody away${at}: ${sentence(view.refused)}`, tone: 'quiet'});
    }

    return [...lines, ...shown];
}

// The panel's alert for a system admin: talking from Matty is switched on
// and its voice port is down, in the server's words. '' when "me" says
// nothing.
export function voiceTalkAlertText(alert: string | undefined): string {
    const words = (alert || '').trim();

    return words === '' ? '' : `Talking from Matty is down: ${sentence(words)}`;
}
