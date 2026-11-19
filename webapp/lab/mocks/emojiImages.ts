import type {SoundEmoji} from '../../src/types';

import e_1f349 from '../emoji/1f349.png';
import e_1f37a from '../emoji/1f37a.png';
import e_1f37f from '../emoji/1f37f.png';
import e_1f389 from '../emoji/1f389.png';
import e_1f3a4 from '../emoji/1f3a4.png';
import e_1f3a7 from '../emoji/1f3a7.png';
import e_1f3ae from '../emoji/1f3ae.png';
import e_1f3af from '../emoji/1f3af.png';
import e_1f3b6 from '../emoji/1f3b6.png';
import e_1f3b7 from '../emoji/1f3b7.png';
import e_1f3b8 from '../emoji/1f3b8.png';
import e_1f3ba from '../emoji/1f3ba.png';
import e_1f3bb from '../emoji/1f3bb.png';
import e_1f3c6 from '../emoji/1f3c6.png';
import e_1f403 from '../emoji/1f403.png';
import e_1f410 from '../emoji/1f410.png';
import e_1f413 from '../emoji/1f413.png';
import e_1f414 from '../emoji/1f414.png';
import e_1f42e from '../emoji/1f42e.png';
import e_1f431 from '../emoji/1f431.png';
import e_1f436 from '../emoji/1f436.png';
import e_1f437 from '../emoji/1f437.png';
import e_1f438 from '../emoji/1f438.png';
import e_1f43a from '../emoji/1f43a.png';
import e_1f440 from '../emoji/1f440.png';
import e_1f44b from '../emoji/1f44b.png';
import e_1f44d from '../emoji/1f44d.png';
import e_1f44e from '../emoji/1f44e.png';
import e_1f44f from '../emoji/1f44f.png';
import e_1f451 from '../emoji/1f451.png';
import e_1f47b from '../emoji/1f47b.png';
import e_1f480 from '../emoji/1f480.png';
import e_1f494 from '../emoji/1f494.png';
import e_1f4a3 from '../emoji/1f4a3.png';
import e_1f4a4 from '../emoji/1f4a4.png';
import e_1f4a5 from '../emoji/1f4a5.png';
import e_1f4aa from '../emoji/1f4aa.png';
import e_1f4af from '../emoji/1f4af.png';
import e_1f4e2 from '../emoji/1f4e2.png';
import e_1f50a from '../emoji/1f50a.png';
import e_1f514 from '../emoji/1f514.png';
import e_1f525 from '../emoji/1f525.png';
import e_1f52b from '../emoji/1f52b.png';
import e_1f602 from '../emoji/1f602.png';
import e_1f605 from '../emoji/1f605.png';
import e_1f62d from '../emoji/1f62d.png';
import e_1f631 from '../emoji/1f631.png';
import e_1f634 from '../emoji/1f634.png';
import e_1f648 from '../emoji/1f648.png';
import e_1f64f from '../emoji/1f64f.png';
import e_1f680 from '../emoji/1f680.png';
import e_1f6a8 from '../emoji/1f6a8.png';
import e_1f6b6 from '../emoji/1f6b6.png';
import e_1f914 from '../emoji/1f914.png';
import e_1f916 from '../emoji/1f916.png';
import e_1f923 from '../emoji/1f923.png';
import e_1f926 from '../emoji/1f926.png';
import e_1f941 from '../emoji/1f941.png';
import e_1f986 from '../emoji/1f986.png';
import e_1f989 from '../emoji/1f989.png';
import e_1f997 from '../emoji/1f997.png';
import e_1f9a2 from '../emoji/1f9a2.png';
import e_1f9c7 from '../emoji/1f9c7.png';
import e_1fa84 from '../emoji/1fa84.png';
import e_231a from '../emoji/231a.png';
import e_23f1_fe0f from '../emoji/23f1-fe0f.png';
import e_2620_fe0f from '../emoji/2620-fe0f.png';
import e_2694_fe0f from '../emoji/2694-fe0f.png';
import e_26a1 from '../emoji/26a1.png';
import e_2728 from '../emoji/2728.png';
import e_2764_fe0f from '../emoji/2764-fe0f.png';
import c_fanfare from '../emoji/custom-fanfare.svg';
import c_wakeup from '../emoji/custom-wakeup.svg';
import c_gg from '../emoji/custom-gg.png';

// The lab's stand-in for Mattermost's emoji pictures and its emoji search.
//
// Swapped in for src/emojiImages.ts (webpack.config.js), as ./client is: the
// lab has no Mattermost behind it, so the pictures it shows are a few of
// Mattermost's own system emoji PNGs (webapp/channels/src/images/emoji at
// v11.11.1, the files /static/emoji/<unified>.png serves), copied into
// lab/emoji and inlined, and three custom emoji made for the lab: a
// fanfare, an alarm clock and a red GG.

export type LabSystemEmoji = {unified: string; names: string[]; category: string; url: string};

export const LAB_SYSTEM_EMOJI: LabSystemEmoji[] = [
    {unified: '1f349', names: ['watermelon'], category: 'food-drink', url: e_1f349},
    {unified: '1f37a', names: ['beer'], category: 'food-drink', url: e_1f37a},
    {unified: '1f37f', names: ['popcorn'], category: 'food-drink', url: e_1f37f},
    {unified: '1f389', names: ['tada'], category: 'activities', url: e_1f389},
    {unified: '1f3a4', names: ['microphone'], category: 'objects', url: e_1f3a4},
    {unified: '1f3a7', names: ['headphones'], category: 'objects', url: e_1f3a7},
    {unified: '1f3ae', names: ['video_game'], category: 'activities', url: e_1f3ae},
    {unified: '1f3af', names: ['dart'], category: 'activities', url: e_1f3af},
    {unified: '1f3b6', names: ['notes'], category: 'objects', url: e_1f3b6},
    {unified: '1f3b7', names: ['saxophone'], category: 'objects', url: e_1f3b7},
    {unified: '1f3b8', names: ['guitar'], category: 'objects', url: e_1f3b8},
    {unified: '1f3ba', names: ['trumpet'], category: 'objects', url: e_1f3ba},
    {unified: '1f3bb', names: ['violin'], category: 'objects', url: e_1f3bb},
    {unified: '1f3c6', names: ['trophy'], category: 'activities', url: e_1f3c6},
    {unified: '1f403', names: ['water_buffalo'], category: 'animals-nature', url: e_1f403},
    {unified: '1f410', names: ['goat'], category: 'animals-nature', url: e_1f410},
    {unified: '1f413', names: ['rooster'], category: 'animals-nature', url: e_1f413},
    {unified: '1f414', names: ['chicken'], category: 'animals-nature', url: e_1f414},
    {unified: '1f42e', names: ['cow'], category: 'animals-nature', url: e_1f42e},
    {unified: '1f431', names: ['cat'], category: 'animals-nature', url: e_1f431},
    {unified: '1f436', names: ['dog'], category: 'animals-nature', url: e_1f436},
    {unified: '1f437', names: ['pig'], category: 'animals-nature', url: e_1f437},
    {unified: '1f438', names: ['frog'], category: 'animals-nature', url: e_1f438},
    {unified: '1f43a', names: ['wolf'], category: 'animals-nature', url: e_1f43a},
    {unified: '1f440', names: ['eyes'], category: 'people-body', url: e_1f440},
    {unified: '1f44b', names: ['wave'], category: 'people-body', url: e_1f44b},
    {unified: '1f44d', names: ['+1', 'thumbsup'], category: 'people-body', url: e_1f44d},
    {unified: '1f44e', names: ['-1', 'thumbsdown'], category: 'people-body', url: e_1f44e},
    {unified: '1f44f', names: ['clap'], category: 'people-body', url: e_1f44f},
    {unified: '1f451', names: ['crown'], category: 'objects', url: e_1f451},
    {unified: '1f47b', names: ['ghost'], category: 'smileys-emotion', url: e_1f47b},
    {unified: '1f480', names: ['skull'], category: 'smileys-emotion', url: e_1f480},
    {unified: '1f494', names: ['broken_heart'], category: 'smileys-emotion', url: e_1f494},
    {unified: '1f4a3', names: ['bomb'], category: 'smileys-emotion', url: e_1f4a3},
    {unified: '1f4a4', names: ['zzz'], category: 'smileys-emotion', url: e_1f4a4},
    {unified: '1f4a5', names: ['boom', 'collision'], category: 'smileys-emotion', url: e_1f4a5},
    {unified: '1f4aa', names: ['muscle'], category: 'people-body', url: e_1f4aa},
    {unified: '1f4af', names: ['100'], category: 'smileys-emotion', url: e_1f4af},
    {unified: '1f4e2', names: ['loudspeaker'], category: 'objects', url: e_1f4e2},
    {unified: '1f50a', names: ['loud_sound'], category: 'objects', url: e_1f50a},
    {unified: '1f514', names: ['bell'], category: 'objects', url: e_1f514},
    {unified: '1f525', names: ['fire'], category: 'travel-places', url: e_1f525},
    {unified: '1f52b', names: ['gun'], category: 'objects', url: e_1f52b},
    {unified: '1f602', names: ['joy'], category: 'smileys-emotion', url: e_1f602},
    {unified: '1f605', names: ['sweat_smile'], category: 'smileys-emotion', url: e_1f605},
    {unified: '1f62d', names: ['sob'], category: 'smileys-emotion', url: e_1f62d},
    {unified: '1f631', names: ['scream'], category: 'smileys-emotion', url: e_1f631},
    {unified: '1f634', names: ['sleeping'], category: 'smileys-emotion', url: e_1f634},
    {unified: '1f648', names: ['see_no_evil'], category: 'smileys-emotion', url: e_1f648},
    {unified: '1f64f', names: ['pray'], category: 'people-body', url: e_1f64f},
    {unified: '1f680', names: ['rocket'], category: 'travel-places', url: e_1f680},
    {unified: '1f6a8', names: ['rotating_light'], category: 'travel-places', url: e_1f6a8},
    {unified: '1f6b6', names: ['walking'], category: 'people-body', url: e_1f6b6},
    {unified: '1f914', names: ['thinking_face', 'thinking'], category: 'smileys-emotion', url: e_1f914},
    {unified: '1f916', names: ['robot_face', 'robot'], category: 'smileys-emotion', url: e_1f916},
    {unified: '1f923', names: ['rolling_on_the_floor_laughing', 'rofl'], category: 'smileys-emotion', url: e_1f923},
    {unified: '1f926', names: ['face_palm'], category: 'people-body', url: e_1f926},
    {unified: '1f941', names: ['drum_with_drumsticks', 'drum'], category: 'objects', url: e_1f941},
    {unified: '1f986', names: ['duck'], category: 'animals-nature', url: e_1f986},
    {unified: '1f989', names: ['owl'], category: 'animals-nature', url: e_1f989},
    {unified: '1f997', names: ['cricket'], category: 'animals-nature', url: e_1f997},
    {unified: '1f9a2', names: ['swan'], category: 'animals-nature', url: e_1f9a2},
    {unified: '1f9c7', names: ['waffle'], category: 'food-drink', url: e_1f9c7},
    {unified: '1fa84', names: ['magic_wand'], category: 'activities', url: e_1fa84},
    {unified: '231a', names: ['watch'], category: 'travel-places', url: e_231a},
    {unified: '23f1-fe0f', names: ['stopwatch'], category: 'travel-places', url: e_23f1_fe0f},
    {unified: '2620-fe0f', names: ['skull_and_crossbones'], category: 'smileys-emotion', url: e_2620_fe0f},
    {unified: '2694-fe0f', names: ['crossed_swords'], category: 'objects', url: e_2694_fe0f},
    {unified: '26a1', names: ['zap'], category: 'travel-places', url: e_26a1},
    {unified: '2728', names: ['sparkles'], category: 'activities', url: e_2728},
    {unified: '2764-fe0f', names: ['heart'], category: 'smileys-emotion', url: e_2764_fe0f},
];

export const LAB_CUSTOM_EMOJI: Array<{id: string; name: string; url: string}> = [
    {id: 'ce-fanfare', name: 'fanfare', url: c_fanfare},
    {id: 'ce-wakeup', name: 'wakeup', url: c_wakeup},
    {id: 'ce-gg', name: 'gg', url: c_gg},
];

const SYSTEM = new Map(LAB_SYSTEM_EMOJI.map((entry) => [entry.unified, entry.url]));
const CUSTOM = new Map(LAB_CUSTOM_EMOJI.map((entry) => [entry.id, entry.url]));

// A picture the lab does not carry is a broken image, as a picture Mattermost
// cannot find is: the tile falls back to its speaker.
export function emojiURL(emoji: SoundEmoji): string {
    if (emoji.kind === 'system') {
        return SYSTEM.get(emoji.unified.toLowerCase()) || `/static/emoji/${emoji.unified.toLowerCase()}.png`;
    }

    return CUSTOM.get(emoji.id) || `/api/v4/emoji/${encodeURIComponent(emoji.id)}/image`;
}

// A system emoji or a custom one, by name, as the server would resolve it.
export function labEmojiNamed(name: string): SoundEmoji | null {
    const system = LAB_SYSTEM_EMOJI.find((entry) => entry.names.includes(name));
    if (system) {
        return {kind: 'system', name, unified: system.unified};
    }
    const custom = LAB_CUSTOM_EMOJI.find((entry) => entry.name === name);

    return custom ? {kind: 'custom', name, id: custom.id} : null;
}

// Mattermost's Emoticon provider, as plugins reach it
// (window.WebappUtils.editor.providers.Emoticon, M7): handlePretextChanged
// takes ':' and at least two letters, matches anywhere in a name, and calls
// back with one group of {name, emoji} items, system emoji carrying unified
// and short_names, custom ones id and name. Answered after a short wait, as
// the real one is after it asks the server for custom emoji.
class LabEmoticonProvider {
    handlePretextChanged(pretext: string, callback: (results: {matchedPretext: string; groups: Array<{key: string; items: unknown[]}>}) => void): boolean {
        const captured = (/(^|\s)(:([^:\s]*))$/).exec(pretext.toLowerCase());
        if (!captured || captured[3].length < 2) {
            return false;
        }

        const partial = captured[3];
        const items: unknown[] = [];
        LAB_SYSTEM_EMOJI.forEach((entry) => {
            const alias = entry.names.find((name) => name.includes(partial));
            if (alias) {
                items.push({name: alias, emoji: {unified: entry.unified.toUpperCase(), short_names: entry.names, category: entry.category}});
            }
        });
        LAB_CUSTOM_EMOJI.forEach((entry) => {
            if (entry.name.includes(partial)) {
                items.push({name: entry.name, emoji: {id: entry.id, name: entry.name, category: 'custom'}});
            }
        });

        window.setTimeout(() => callback({matchedPretext: captured[2], groups: [{key: 'emojis', items}]}), 120);

        return true;
    }
}

export function installLabEmoticon(): void {
    const win = window as unknown as {WebappUtils?: Record<string, unknown>};
    win.WebappUtils = {...(win.WebappUtils || {}), editor: {providers: {Emoticon: LabEmoticonProvider}}};
}
