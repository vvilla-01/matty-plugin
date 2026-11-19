import {emoticonProvider, recentEmojiNames} from './mattermost';
import type {EmoticonItem} from './mattermost';
import {SUGGESTED_EMOJI} from './soundLogic';
import type {SoundEmoji} from './types';

// Choosing a sound's emoji (design/3.2.0.md 7.13 step 3): Mattermost's own
// emoji search, which plugins may use though they cannot open its picker
// (M7), and the reader's recent emoji from Mattermost's store (M8). Whatever
// is chosen goes to the server by kind and name only; the server looks it up
// itself and keeps its own answer (7.8).

export const emojiKey = (emoji: SoundEmoji): string => (emoji.kind === 'system' ? `s:${emoji.unified}` : `c:${emoji.id}`);

function fromItem(item: EmoticonItem): SoundEmoji | null {
    if (item.emoji.unified) {
        return {kind: 'system', name: item.name, unified: item.emoji.unified.toLowerCase()};
    }
    if (item.emoji.id) {
        return {kind: 'custom', name: item.name, id: item.emoji.id};
    }

    return null;
}

const suggested = (): SoundEmoji[] => SUGGESTED_EMOJI.map(({name, unified}) => ({kind: 'system', name, unified}));

// The forty suggested for sounds, as emoji.
export const SUGGESTED: SoundEmoji[] = suggested();

// What a search finds, through the provider; answer is called once. Without
// the provider (a Mattermost that dropped it), the suggested list filtered by
// name. Fewer than two letters asks nothing, as the provider would not either.
export function searchEmoji(query: string, answer: (found: SoundEmoji[]) => void): void {
    const words = query.trim().toLowerCase().replace(/^:|:$/g, '');
    const Provider = emoticonProvider();
    if (!Provider) {
        answer(SUGGESTED.filter((emoji) => emoji.name.includes(words)));

        return;
    }
    if (words.length < 2) {
        answer([]);

        return;
    }

    let answered = false;
    const once = (found: SoundEmoji[]) => {
        if (!answered) {
            answered = true;
            answer(found);
        }
    };

    try {
        const asked = new Provider().handlePretextChanged(`:${words}`, (results) => {
            const items = (results.groups || []).flatMap((group) => group.items || []);
            once(items.map(fromItem).filter((emoji): emoji is SoundEmoji => emoji !== null));
        });
        if (!asked) {
            once([]);
        }
    } catch {
        once(SUGGESTED.filter((emoji) => emoji.name.includes(words)));
    }
}

// The reader's recent emoji, resolved through the same search, one exact
// match each, at most eight.
export function recentEmoji(answer: (recent: SoundEmoji[]) => void): void {
    const names = recentEmojiNames().slice(0, 8);
    if (names.length === 0) {
        answer([]);

        return;
    }

    const resolved: Array<SoundEmoji | null> = names.map(() => null);
    let waiting = names.length;
    names.forEach((name, index) => searchEmoji(name, (list) => {
        resolved[index] = list.find((emoji) => emoji.name === name) || null;
        waiting -= 1;
        if (waiting === 0) {
            answer(resolved.filter((emoji): emoji is SoundEmoji => emoji !== null));
        }
    }));
}
