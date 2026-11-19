import type {Snapshot, Voice, VoiceChannel, VoicePerson} from './types';

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

// One channel of the In voice row and the people in it. channel is absent
// for a server before 4.3.0, which sends no channels: everybody is then one
// group with no name over it.
export type VoiceGroup = {
    channel?: VoiceChannel;
    people: VoicePerson[];
};

// voiceGroups is who is in voice, by channel, in the order the server sends
// the channels (a channel before the ones under it), each with its people in
// the order the server sends them. Channels nobody is in are left out, and
// so is everything while the plugin is not connected.
//
// Somebody in a channel the list does not hold is put with the root, so that
// nobody connected is left off the row.
export function voiceGroups(voice?: Voice): VoiceGroup[] {
    const people = voice?.connected ? voice.people || [] : [];
    if (people.length === 0) {
        return [];
    }

    const channels = voice?.channels || [];
    if (channels.length === 0) {
        return [{people: people.slice()}];
    }

    const known = new Set(channels.map((channel) => channel.id));
    const root = channels.find((channel) => channel.parent === undefined)?.id ?? 0;
    const byChannel = new Map<number, VoicePerson[]>();
    const nowhere: VoicePerson[] = [];
    people.forEach((person) => {
        let id = person.channel ?? 0;
        if (!known.has(id)) {
            id = root;
        }
        if (!known.has(id)) {
            nowhere.push(person);
            return;
        }
        byChannel.set(id, (byChannel.get(id) || []).concat(person));
    });

    const groups: VoiceGroup[] = channels.
        filter((channel) => (byChannel.get(channel.id) || []).length > 0).
        map((channel) => ({channel, people: byChannel.get(channel.id) || []}));
    if (nowhere.length > 0) {
        groups.push({people: nowhere});
    }

    return groups;
}

// The mark beside a name in the In voice row: crossed headphones for
// somebody deafened, a crossed microphone for somebody muted (or not allowed
// to speak where they are), nothing for somebody who may be heard.
export type VoiceMark = 'deaf' | 'muted' | '';

export function voiceMark(person: VoicePerson): VoiceMark {
    if (person.deaf) {
        return 'deaf';
    }

    return person.muted ? 'muted' : '';
}

// What a name in the In voice row says when pointed at, and to a screen
// reader.
export function voicePersonLabel(person: VoicePerson): string {
    switch (voiceMark(person)) {
    case 'deaf':
        return `${person.name}, deafened`;
    case 'muted':
        return `${person.name}, muted`;
    default:
        return person.name;
    }
}
