import type React from 'react';

import {AdminAccess} from './components/AdminAccess';
import {AdminAppCheck} from './components/AdminAppCheck';
import {AdminAirtime} from './components/AdminAirtime';
import {AdminBirthdays} from './components/AdminBirthdays';
import {AdminBotAvatar} from './components/AdminBotAvatar';
import {AdminBrand} from './components/AdminBrand';
import {AdminChannelPictures} from './components/AdminChannelPictures';
import {AdminClips} from './components/AdminClips';
import {AdminMediaCache} from './components/AdminMediaCache';
import {AdminPush} from './components/AdminPush';
import {AdminSettingsBackup} from './components/AdminSettingsBackup';
import {AdminSounds} from './components/AdminSounds';
import {AdminStills} from './components/AdminStills';
import {AdminTheme} from './components/AdminTheme';
import {AdminTwitch} from './components/AdminTwitch';
import {AdminSteam} from './components/AdminSteam';
import {AdminHytale} from './components/AdminHytale';

// The plugin's own controls on System Console > Plugins > Matty, one per
// "custom" setting in plugin.json. index.tsx registers exactly this list, and
// the lab's copy of the page renders exactly this list, so the two cannot
// drift apart.
//
// Mattermost looks a component up by the LOWERCASED manifest key, so
// 'airtimetools' matches the "AirtimeTools" setting. showTitle puts the
// setting's display name in the label column and its help text under the
// component; without it the component gets the whole width and neither.
export type AdminSetting = {
    key: string;
    component: React.ComponentType<any>;
    showTitle: boolean;
};

export const ADMIN_SETTINGS: AdminSetting[] = [
    // Who is let in to watch without a bearer token, and unlinking somebody's
    // stream (design/lock-screen.md 8.1). Admin routes only, so it works for
    // a system admin whose own panel is locked.
    {key: 'accesslist', component: AdminAccess, showTitle: true},

    // The app check: this plugin's own release in one line, and who has
    // proved a genuine Matty and who has not. Read-only.
    {key: 'appchecklist', component: AdminAppCheck, showTitle: true},

    // Push notifications: whether pushes can go out, and how many phones get
    // them, by push server, never by person. Read-only.
    {key: 'pushstatus', component: AdminPush, showTitle: true},

    // Recorded airtime: what is stored, taking a copy out, putting one back.
    {key: 'airtimetools', component: AdminAirtime, showTitle: true},

    // Adding, editing and removing a birthday happens here and nowhere else:
    // the System Console is behind manage_system, so the list is admin-only by
    // construction rather than by a check.
    {key: 'birthdays', component: AdminBirthdays, showTitle: true},
    {key: 'botavatar', component: AdminBotAvatar, showTitle: true},
    {key: 'themetools', component: AdminTheme, showTitle: true},

    // Backing the settings up and putting them back.
    {key: 'settingsbackup', component: AdminSettingsBackup, showTitle: true},

    // The emblem, the App Bar glyph and the ghost. Stored by the plugin rather
    // than in the configuration, so like the bot's picture they save
    // themselves and never touch the page's Save button.
    {key: 'artwork', component: AdminBrand, showTitle: true},
    {key: 'channelpictures', component: AdminChannelPictures, showTitle: true},

    // The preview pictures on the Live now cards, and who took them: take one
    // down, or stop somebody sharing them.
    {key: 'previewpictures', component: AdminStills, showTitle: true},

    // Clips on the server: every clip kept, who made them, what is being
    // recorded, and who is stopped from clipping. For any system admin,
    // whether or not they have linked a stream, and with its own player, since
    // the console renders none of the places the Clips dialog lives.
    {key: 'clipsadmin', component: AdminClips, showTitle: true},

    // Remembered titles (media lookups): how much the server keeps of what it
    // looked up, and forgetting it.
    {key: 'mediacache', component: AdminMediaCache, showTitle: true},

    // Twitch connections (design/twitch-live.md 2.4): the OAuth Redirect URL
    // to add to the Twitch application, who has connected, disconnecting and
    // blocking them, and deleting everything the plugin holds from Twitch.
    // Admin routes only, and all of it works whatever the switch says.
    {key: 'twitchsetup', component: AdminTwitch, showTitle: true},

    // Steam connections and Hytale connections (design/3.4.0.md 3.8 and
    // 4.7), in Twitch's card's idiom: where each service sends people back
    // to, whether it is set up, who has connected, disconnecting and
    // blocking them, and deleting everything the plugin holds from it. Admin
    // routes only, all of it whatever the switches say, and nobody's Steam
    // status, since the cards work for a locked admin.
    {key: 'steamsetup', component: AdminSteam, showTitle: true},
    {key: 'hytalesetup', component: AdminHytale, showTitle: true},

    // Sounds on the server (design/3.2.0.md 7.11): every sound on the
    // soundboard, with Listen and Delete; who is blocked from it; whether
    // the plugin's Mumble account can play; and starting the board again when
    // its list cannot be read.
    {key: 'soundsadmin', component: AdminSounds, showTitle: true},
];
