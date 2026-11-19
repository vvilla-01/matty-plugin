import {staticURL} from './manifest';

// The plugin's icon: the App Bar button, the channel header's button, the
// side panel's title, the small player's Back button and the Settings
// dialog's tab.
//
// Fetched from the plugin rather than inlined, because an admin can replace
// it from the System Console. The URL is stable and only its CONTENTS
// change, which is what lets registerAppBarComponent, which takes a URL
// once, at load, pick up new artwork at all. It always answers: Matty's own
// icon (server/static/matty-icon.svg) where nothing has been uploaded.
export const APP_BAR_ICON = staticURL('brand/icon');
