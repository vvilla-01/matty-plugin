import {accessEventHeard} from './access';
import {accountLinkHeard} from './accounts';
import {PLUGIN_ID} from './manifest';
import {takeBrand} from './name';
import {twitchLinkHeard} from './twitch';

// Sent to one person when what the server holds about them may have
// changed: their access (a link, an unlink, a let-in), or one of their own
// connections (Twitch, Steam, Hytale: connected, disconnected, blocked,
// taken over); and to everybody when an admin deletes all of a service's
// data, and when the server's name for the plugin or its artwork changes. It
// carries the name and the artwork, which are taken at once (name.ts), and
// nothing about anybody: otherwise it says only "read me again" (meHeard,
// below), so it can never be what unlocks anybody.
export const ME_EVENT = `custom_${PLUGIN_ID}_me`;

// The "me" event: "me" is read again (access.ts, which also asks a little
// later whether the reader's access is still what was read), and whoever was
// waiting on a connection of the reader's own is told to look: the Connect
// Twitch sheet, and the Steam and Hytale flows, which also read everybody's
// accounts again, since the reader's own row is among them. Each looks at the
// "me" read after the event for what it was waiting for, so being told about
// a change that was somebody else's costs a look and nothing more.
//
// Installed by index.tsx for the page, and by the lab, whose buttons for an
// event from the server press this very handler. Mattermost hands it the
// event, whose data is what the server sent.
export function meHeard(message?: unknown): void {
    takeBrand((message as {data?: unknown} | undefined)?.data);
    const read = accessEventHeard();
    twitchLinkHeard(read);
    accountLinkHeard(read);
}
