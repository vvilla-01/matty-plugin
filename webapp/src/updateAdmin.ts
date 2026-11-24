import type {AdminUpdate} from './client';

// The words of System Console > Plugins > Matty > Updates > Status
// (components/AdminUpdate.tsx), its Install, and the update alert in the
// panel's admin alerts (components/AdminAlerts.tsx), worked out from what
// the server sends (GET /api/v1/admin/update, and "me"'s updateAlert). It
// imports nothing but types, so build/check-update.mjs runs it as it is
// written.

// The section's title in plugin.json, where the card and the setting are.
export const UPDATE_SECTION = 'Updates';

// What the releases are called, whatever this server calls the plugin.
export const UPDATE_PRODUCT = 'Matty Plugin';

// Where every release's page is. The server builds the address itself; the
// panel links only to one there, and shows anything else as text.
export const UPDATE_PAGES = 'https://github.com/vvilla-01/matty-plugin/';

// A line of the card, and how it is worn: the page's own colour, quieter, or
// the danger red.
export type UpdateLine = {text: string; tone: 'plain' | 'quiet' | 'error'};

// What "me" says to a system admin: a newer release is out, and its page.
export type UpdateAlert = {version: string; url: string};

// Whether an address is a release's page, which the panel may link to.
export function updatePageLinkable(url: string | undefined): boolean {
    return typeof url === 'string' && url.startsWith(UPDATE_PAGES) && !/[\s"'<>]/.test(url);
}

// The alert in the panel, or null when there is nothing to say.
export function updateAlertText(alert: UpdateAlert | undefined): {text: string; url: string; linked: boolean} | null {
    if (!alert || !alert.version) {
        return null;
    }

    return {
        text: `${UPDATE_PRODUCT} ${alert.version} is out.`,
        url: alert.url || '',
        linked: updatePageLinkable(alert.url),
    };
}

// The card's head: what this server runs.
export function updateHead(view: AdminUpdate): string {
    return `This server runs ${UPDATE_PRODUCT} ${view.running}.`;
}

// The card's lines, in order: what the setting does, what the last look
// found and when, why a newer version cannot be installed from here, and
// the last trouble, left out while the Install's step line says the same
// words (failed, the step's sentence). when says when an instant was
// (time.ts postedWhen), passed in so this file needs nothing of the page.
export function updateLines(view: AdminUpdate, when: (at: number) => string, failed?: string): UpdateLine[] {
    const lines: UpdateLine[] = [];

    if (view.enabled) {
        lines.push({text: 'Once a day it looks on GitHub for a newer release, and tells the system admins.', tone: 'plain'});
    } else {
        lines.push({text: 'Look for updates is off: nothing is asked of GitHub about Matty Plugin’s releases.', tone: 'plain'});
    }

    if (!view.lookedAt) {
        lines.push({text: view.enabled ? 'It has not looked yet.' : 'It has not looked.', tone: 'quiet'});
    } else if (!view.latest) {
        lines.push({text: `No release was found. Last looked: ${when(view.lookedAt)}.`, tone: 'quiet'});
    } else if (view.newer) {
        lines.push({text: `${UPDATE_PRODUCT} ${view.latest} is out. Last looked: ${when(view.lookedAt)}.`, tone: 'plain'});
    } else {
        lines.push({text: `This is the newest release. Last looked: ${when(view.lookedAt)}.`, tone: 'quiet'});
    }

    if (view.enabled && view.newer && view.latest && view.install && !view.install.ready) {
        for (const missing of view.install.missing || []) {
            const text = missingText(missing, view.latest, view.platform);
            if (text) {
                lines.push({text, tone: 'plain'});
            }
        }
    }

    if (view.trouble && !(failed && sentence(failed) === sentence(view.trouble))) {
        lines.push({text: view.troubleAt ? `${when(view.troubleAt)}: ${sentence(view.trouble)}` : sentence(view.trouble), tone: 'error'});
    }

    return lines;
}

// Why a newer version cannot be installed from here, as the server names
// it, in the words its refusal uses. platform is this server's, which the
// card always carries.
export function missingText(missing: string, version: string, platform?: string): string {
    switch (missing) {
    case 'unsigned':
        return `This copy of ${UPDATE_PRODUCT} is not a signed release, so it does not install releases. Install ${version} by hand from its page.`;
    case 'asset':
        return platform ? `${UPDATE_PRODUCT} ${version} has no bundle for this server’s platform (${platform}).` : `${UPDATE_PRODUCT} ${version} has no bundle for this server’s platform.`;
    case 'uploads':
        return 'Mattermost does not take plugin uploads here: in System Console > Plugins > Plugin Management, Enable Plugins and Enable Plugin Uploads must be on and Require Plugin Signature off.';
    default:
        return '';
    }
}

// ---- Install ----

// A version as the plugin numbers its releases.
const UPDATE_VERSION = /^(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})$/;

// Why the bundle route's answer must not be uploaded, or '' when it may:
// anything but 200 with at least one byte and a version (the plugin
// stopping in the middle answers 503, never this), or a version other than
// the one the admin was asked about, which another look found meanwhile.
// Nothing is uploaded then, and the card is read again. That bundle has
// spent the minute's claim, so the next press works a minute later.
export function bundleProblem(status: number, bytes: number, version: string, asked: string): string {
    if (status !== 200 || !(bytes >= 1) || !UPDATE_VERSION.test(version)) {
        return 'The bundle did not arrive whole. Try again in a moment.';
    }
    if (version !== asked) {
        return `A newer version, ${version}, was found meanwhile. Press Install again in a minute.`;
    }

    return '';
}

// Where an install stands: nothing yet; asking first; getting the checked
// bundle from the plugin; handing it to Mattermost; done; or failed, with
// the sentence the plugin or Mattermost gave.
export type InstallStep = 'idle' | 'asking' | 'fetching' | 'handing' | 'done' | 'failed';
export type InstallState = {step: InstallStep; error?: string};

export type InstallEvent =
    {kind: 'press'} | {kind: 'cancel'} | {kind: 'confirm'} | {kind: 'fetched'} | {kind: 'handed'} |
    {kind: 'failed'; error: string};

export const INSTALL_IDLE: InstallState = {step: 'idle'};

// The next state, or the same one when the event does not belong to it: a
// press while a step runs does nothing.
export function installNext(state: InstallState, event: InstallEvent): InstallState {
    switch (event.kind) {
    case 'press':
        return state.step === 'idle' || state.step === 'failed' ? {step: 'asking'} : state;
    case 'cancel':
        return state.step === 'asking' ? INSTALL_IDLE : state;
    case 'confirm':
        return state.step === 'asking' ? {step: 'fetching'} : state;
    case 'fetched':
        return state.step === 'fetching' ? {step: 'handing'} : state;
    case 'handed':
        return state.step === 'handing' ? {step: 'done'} : state;
    case 'failed':
        return state.step === 'fetching' || state.step === 'handing' ? {step: 'failed', error: event.error.trim() || 'It could not be installed.'} : state;
    default:
        return state;
    }
}

// Whether the card offers Install: the setting on, a newer version known,
// and a bundle of it to be had from here.
export function installOffered(view: AdminUpdate | null): boolean {
    return Boolean(view && view.enabled && view.newer && view.latest && view.install?.ready);
}

export function installButtonText(version: string): string {
    return `Install ${version}`;
}

// The version the Install button names, the one a press installs: while a
// step runs, the one being installed (installing); otherwise, after a
// failure too, the card's newest (latest), which the next press asks about.
export function installButtonVersion(state: InstallState, installing: string, latest: string): string {
    return (state.step === 'fetching' || state.step === 'handing') && installing ? installing : latest;
}

// What the card asks before it installs, one line each.
export function installQuestion(version: string, inVoice: number | undefined): string[] {
    const lines = [`Install ${UPDATE_PRODUCT} ${version}? The plugin restarts, which takes a few seconds.`];
    const phones = inVoice || 0;
    if (phones === 1) {
        lines.push('1 phone is in voice through Matty and will be dropped.');
    } else if (phones > 1) {
        lines.push(`${phones} phones are in voice through Matty and will be dropped.`);
    }

    return lines;
}

// The line under the buttons for a step, or '' for none.
export function installStepText(state: InstallState, version: string): string {
    switch (state.step) {
    case 'fetching':
        return 'Getting and checking the bundle…';
    case 'handing':
        return 'Handing it to Mattermost…';
    case 'done':
        return `${UPDATE_PRODUCT} ${version} is installed. Reload this page to see its panel.`;
    case 'failed':
        return state.error || '';
    default:
        return '';
    }
}

// Which buttons the card shows for a state: Install (disabled while a step
// runs), or Install and Cancel while it asks, or none once it is done.
export function installButtons(state: InstallState): {install: boolean; confirm: boolean; disabled: boolean} {
    switch (state.step) {
    case 'asking':
        return {install: false, confirm: true, disabled: false};
    case 'fetching':
    case 'handing':
        return {install: true, confirm: false, disabled: true};
    case 'done':
        return {install: false, confirm: false, disabled: true};
    default:
        return {install: true, confirm: false, disabled: false};
    }
}

// The server's words as a sentence, ending in a full stop whatever they
// ended with.
function sentence(words: string): string {
    const trimmed = words.trim();

    return /[.!?…]$/.test(trimmed) ? trimmed : `${trimmed}.`;
}
