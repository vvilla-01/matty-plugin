import type {AdminAppCheck, AdminAppCheckVerdict} from './client';

// The words and rules of System Console > Plugins > Matty > App check that
// need nothing of the page: the Minimum Matty version editor
// (components/AdminMinimum.tsx), read from and written to the string the
// setting stores, and what the Results card (components/AdminAppCheck.tsx)
// says of the minimum, from GET /api/v1/admin/appcheck. It imports nothing
// but types, so build/check-appcheck.mjs runs it as it is written.

// ---- The editor ----

// The setting's three values: blank for no minimum, "latest" for the newest
// release of Matty, or a version N.N.N.
export type MinimumChoice = 'none' | 'latest' | 'version';

export const MINIMUM_CHOICES: Array<{choice: MinimumChoice; words: string}> = [
    {choice: 'none', words: 'No minimum'},
    {choice: 'latest', words: 'The latest release of Matty'},
    {choice: 'version', words: 'This version or newer'},
];

const MINIMUM_LATEST = 'latest';

// A version as the server reads one (server/update.go updateVersionRE).
const MINIMUM_VERSION = /^(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})$/;

// What the server's strings.TrimSpace takes off either end: Unicode's white
// space, which is not quite what String.trim takes (it leaves U+0085 and
// takes U+FEFF). Built from the code points, since a line or paragraph
// separator written into a regular expression ends it.
const GO_SPACES = [0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0x85, 0xa0, 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000];

const isGoSpace = (unit: number): boolean => GO_SPACES.includes(unit);

function trimmed(text: string): string {
    let start = 0;
    let end = text.length;
    while (start < end && isGoSpace(text.charCodeAt(start))) {
        start += 1;
    }
    while (end > start && isGoSpace(text.charCodeAt(end - 1))) {
        end -= 1;
    }

    return text.slice(start, end);
}

// Whether the server takes a value for the latest release: "latest" in any
// case (strings.EqualFold, under which a long s is an s), spaces around it ignored.
const isLatest = (text: string): boolean => trimmed(text).toLowerCase().replace(/\u017f/g, 's') === MINIMUM_LATEST;

// The choice a stored value shows, and what its box holds: as the server
// reads it, so "LATEST" or "Latest " is the latest release and " 0.1.33 " is
// 0.1.33 or newer; a value the server counts as none is shown as typed,
// under "This version or newer", and is never replaced.
export function minimumChoiceOf(stored: string | undefined): {choice: MinimumChoice; typed: string} {
    const value = stored || '';
    const plain = trimmed(value);
    if (plain === '') {
        return {choice: 'none', typed: ''};
    }
    if (isLatest(value)) {
        return {choice: 'latest', typed: ''};
    }
    if (MINIMUM_VERSION.test(plain)) {
        return {choice: 'version', typed: plain};
    }

    return {choice: 'version', typed: value};
}

// What a choice stores, typed being what the box holds: "This version or
// newer" with the box empty stores "", which saves no minimum.
export function minimumStored(choice: MinimumChoice, typed: string): string {
    switch (choice) {
    case 'latest':
        return MINIMUM_LATEST;
    case 'version':
        return typed;
    default:
        return '';
    }
}

// The line under the box, or null for a version.
export function minimumTypedNote(typed: string): {tone: 'quiet' | 'error'; text: string} | null {
    const plain = trimmed(typed);
    if (plain === '') {
        return {tone: 'quiet', text: 'Saved empty, this is no minimum.'};
    }
    if (MINIMUM_VERSION.test(plain)) {
        return null;
    }

    return {tone: 'error', text: `“${plain}” is not a version such as 0.1.33, so it counts as no minimum.`};
}

// ---- The Results card ----

export type AppCheckLine = {text: string; tone: 'plain' | 'error'};

// What the card says of the minimum, in order: the one in force and where it
// comes from, with the last look for the latest release and its last
// trouble; then, while one is in force, what it refuses and what it goes
// by. when says when an
// instant was (time.ts postedWhen), passed in so this file needs nothing of
// the page.
export function minimumLines(view: AdminAppCheck, when: (at: number) => string): AppCheckLine[] {
    const lines: AppCheckLine[] = [];

    if (view.minimumFrom === 'latest') {
        const latest = view.latest || {};
        if (latest.version) {
            const looked = latest.lookedAt ? ` Last looked: ${when(latest.lookedAt)}.` : '';
            lines.push({text: `The oldest Matty this server answers: ${latest.version}, the latest release.${looked}`, tone: 'plain'});
        } else {
            lines.push({text: 'Minimum Matty version is the latest release, and this server does not know it yet, so no minimum is in force.', tone: 'plain'});
        }
        if (latest.trouble) {
            lines.push({text: latest.troubleAt ? `${when(latest.troubleAt)}: ${sentence(latest.trouble)}` : sentence(latest.trouble), tone: 'error'});
        }
    } else if (view.minimum) {
        lines.push({text: `The oldest Matty this server answers: ${view.minimum}.`, tone: 'plain'});
    }

    if (view.minimum) {
        lines.push({text: `A Matty older than ${view.minimum}, or one whose version cannot be read, is refused by this plugin's API everywhere but the few places it needs to show why.`, tone: 'plain'});
        lines.push(view.byWord ? {
            text: 'The minimum goes by what each Matty says of itself, so a changed copy can say any version. With Check the Matty app at “Check, and refuse a Matty that fails”, it goes by what the phone proved.',
            tone: 'plain',
        } : {
            text: 'The minimum goes by what each phone proved in its check.',
            tone: 'plain',
        });
    }

    return lines;
}

// Whether the card offers Check now: only while the setting is the latest
// release, which the server refuses otherwise.
export function latestCheckOffered(view: AdminAppCheck | null): boolean {
    return view?.minimumFrom === 'latest';
}

// A verdict's version, as its row shows it: the version the app said as it
// proved itself, with the version code Android attested beside it.
export function verdictVersion(verdict: Pick<AdminAppCheckVerdict, 'said' | 'versionCode'>): string {
    if (verdict.said) {
        return verdict.versionCode ? `Matty ${verdict.said} (${verdict.versionCode})` : `Matty ${verdict.said}`;
    }

    return verdict.versionCode ? `app version code ${verdict.versionCode}` : '';
}

// The server's words as a sentence, ending in a full stop whatever they
// ended with.
function sentence(words: string): string {
    const plain = words.trim();

    return /[.!?…]$/.test(plain) ? plain : `${plain}.`;
}
