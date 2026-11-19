import {APP_BAR_ICON} from './icon';
import {PLUGIN_ID} from './manifest';
import {pluginPreference} from './mattermost';

import type {Settings} from './awayLogic';

// The plugin's two per-person settings, under Settings › Plugin preferences
// › <the server's name for it>, which Mattermost draws from this schema
// (registerUserSettings). Both are opt-outs and both default to on: the
// readers below fall back to the defaults when nothing has been stored.
//
// A radio setting needs a name, a default that is not empty, and its options
// (types/plugins/user_settings.ts; utils/plugins/plugin_setting_extraction.tsx).
// The tab's name must be a plain string, so the schema is made for a name
// (name.ts) and registered again whenever the name changes (index.tsx):
// Mattermost replaces the plugin's entry in place.
export function userSettings(name: string, icon: string = APP_BAR_ICON) {
    return {
        id: PLUGIN_ID,
        uiName: name,
        icon,
        sections: [
            {
                title: 'After replying to a message',
                settings: [{
                    name: 'return_after_reply',
                    type: 'radio' as const,
                    default: 'reply',
                    helpText: `Only when a thread or another of Mattermost’s own panels took ${name}’s place. If you closed ${name} yourself, it stays closed.`,
                    options: [
                        {value: 'reply', text: 'As soon as your reply is posted', helpText: 'Closing the thread brings it back too.'},
                        {value: 'close', text: 'When you close the thread'},
                        {value: 'never', text: `Never, I’ll open ${name} myself`},
                    ],
                }],
            },
            {
                title: 'While something else is in the side panel',
                settings: [{
                    name: 'mini_player',
                    type: 'radio' as const,
                    default: 'on',
                    helpText: 'Only while a stream is playing. It goes back into the panel without reconnecting.',
                    options: [
                        {value: 'on', text: 'Keep watching in a small player over the channel'},
                        {
                            value: 'off',
                            text: 'Only watch in the side panel',
                            helpText: 'The stream is let go a minute after something else takes the panel.',
                        },
                    ],
                }],
            },
        ],
    };
}

export type UserSettings = ReturnType<typeof userSettings>;

// Read every time they are needed rather than kept, so a change made in the
// settings applies at once, even in the middle of a detour. A value this
// version does not know falls back to the default.
export function returnSetting(): Settings['returnAfter'] {
    const value = pluginPreference('return_after_reply', 'reply');

    return value === 'close' || value === 'never' ? value : 'reply';
}

export function miniSetting(): Settings['mini'] {
    return pluginPreference('mini_player', 'on') === 'off' ? 'off' : 'on';
}

export function currentSettings(): Settings {
    return {returnAfter: returnSetting(), mini: miniSetting()};
}
