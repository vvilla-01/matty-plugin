import type {Snapshot, VoicePerson} from './types';

// Whether somebody is on Mumble right now, from the snapshot every client
// already has — the same roster the voice row at the top of the panel shows,
// so nobody sees anything here they could not already see there.
//
// Only people the plugin has matched to a Mattermost account can be found:
// a Mumble name with no account behind it has nobody's profile to go on. And
// nothing at all while the plugin is not connected — "not connected" is not
// "nobody is in there", and the panel does not claim to know either way.
export function voiceOf(snapshot: Snapshot, userId?: string): VoicePerson | null {
    if (!userId || !snapshot.voice?.connected) {
        return null;
    }

    // Somebody can be on more than once — a phone and a desktop, say, both
    // matched to the same account. They are talking if ANY of them is, so
    // they only read as muted when every one of them is. The server works
    // that out once (the snapshot's people index, server/people.go
    // voiceByUser), and every client reads the same answer.
    const state = snapshot.people?.[userId]?.voice;
    if (!state) {
        return null;
    }

    const first = (snapshot.voice.people || []).find((person) => person.userId === userId);

    return {name: first?.name || '', userId, muted: state === 'muted'};
}

// What the headphones say when pointed at, and to a screen reader.
export function voiceLabel(person: VoicePerson): string {
    return person.muted ? 'On Mumble, muted' : 'On Mumble';
}
