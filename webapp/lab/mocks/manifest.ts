import oswald from '../../../server/static/fonts/oswald-600.woff2';
import icon from '../../../server/static/matty-icon.svg';
import mark from '../pictures/emblem.svg';
import ghost from '../pictures/ghost.svg';
import manifestJson from '../../../plugin.json';

// The real manifest points the stylesheet at the plugin's own /static endpoint.
// There is no plugin serving anything in the lab, so the fonts and the
// pictures are inlined into the bundle instead, and the panel is drawn in the
// typeface it ships with rather than in whatever the machine happens to fall
// back to. The icon is the one the plugin ships (Matty's); the emblem and the
// ghost, which the plugin does not ship, are plain stand-ins of the lab's own
// (lab/pictures), drawn only while the lab's server says it has them.
//
// Still no external request: a data URI is the bundle, not the network.
// The id is plugin.json's, as the real manifest.ts spells it.
export const PLUGIN_ID: string = (manifestJson as {id: string}).id;

export const PLUGIN_BASE = '/lab';

// Keyed by the path the real staticURL is asked for. In production brand/icon
// is an admin's upload or the built-in icon, and brand/logo and brand/ghost an
// admin's upload or nothing (404); in the lab there is no store, so these ARE
// the answer.
const INLINED: Record<string, string> = {
    'fonts/oswald-600.woff2': oswald,
    'brand/logo': mark,
    'brand/icon': icon,
    'brand/ghost': ghost,
};

export const BUILT_IN_BRAND: Record<string, string> = {
    logo: mark,
    icon: icon,
    ghost: ghost,
};

export const staticURL = (file: string): string => INLINED[file] || `${PLUGIN_BASE}/static/${file}`;
