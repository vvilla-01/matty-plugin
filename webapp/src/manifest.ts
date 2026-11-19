// Must match the id in plugin.json.
export const PLUGIN_ID = 'ceo.villa.matty';

export const PLUGIN_BASE = `/plugins/${PLUGIN_ID}`;

export const staticURL = (file: string): string => `${PLUGIN_BASE}/static/${file}`;
