import type {SoundEmoji} from './types';

// Where an emoji's picture is: always on Mattermost's own origin, never
// anybody else's (design/3.2.0.md M8, A13). A system emoji is Mattermost's
// 64 px PNG, filed under its unified code; one of the server's own is streamed
// by Mattermost from its emoji route, and may be an animated GIF up to 128 px.
//
// Its own module so the lab, which has no Mattermost behind it, can swap in
// the few pictures it carries (lab/mocks/emojiImages.ts), as it swaps
// ./client.
export function emojiURL(emoji: SoundEmoji): string {
    if (emoji.kind === 'system') {
        return `/static/emoji/${emoji.unified.toLowerCase()}.png`;
    }

    return `/api/v4/emoji/${encodeURIComponent(emoji.id)}/image`;
}
