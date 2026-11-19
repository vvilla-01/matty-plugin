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

// The card's lines: the switch, the port and the addresses phones are given
// or why they are not, the voice server, who is talking from Matty now, the
// budget, and the voice server's last refusal. when is how the card says a
// time (time.ts postedWhen), passed in so this file needs nothing of the
// page.
export function voiceAdminLines(view: AdminVoice, when: (at: number) => string): VoiceAdminLine[] {
    if (!view.on) {
        return [{text: 'Talking from Matty is off: the app shows who is in voice, and nobody can join a channel from it.', tone: 'plain'}];
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

    return lines;
}

// The panel's alert for a system admin: talking from Matty is switched on
// and its voice port is down, in the server's words. '' when "me" says
// nothing.
export function voiceTalkAlertText(alert: string | undefined): string {
    const words = (alert || '').trim();

    return words === '' ? '' : `Talking from Matty is down: ${sentence(words)}`;
}
