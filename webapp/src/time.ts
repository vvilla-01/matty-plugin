// Turning one stored instant into the right words for whoever is reading.
//
// The server stores epoch milliseconds and nothing else time-like. It never
// formats a date and never learns a zone, because the snapshot is built once
// and pushed to everybody: a formatted time in it would be right for at most
// one reader, and a "starting soon" flag would change on every poll and
// broadcast a message to every client for ever.
//
// So all of it happens here, with Intl, which every browser ships. No timezone
// database is downloaded — the plugin is not allowed to fetch anything.

// Intl's formatters, kept once made: one for each language, zone and set of
// options. Making one is the slow part, some 70 µs against a microsecond or
// two to use it, and an Up next card asks for several on every snapshot push,
// as an untitled clip does on every tick of the open Clips list. There are
// only ever a few, as the options are this file's own handful and the zone is
// the reader's.
//
// One with no zone is made every time instead. It takes the machine's zone as
// it is made, so a kept one would go on in that zone after the machine's had
// changed. A zone Intl does not know throws from the constructor, before
// anything is kept, so the callers' fallbacks see it as they always have.
const formats = new Map<string, Intl.DateTimeFormat>();

function formatter(locale: string | undefined, options: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
    if (!options.timeZone) {
        return new Intl.DateTimeFormat(locale, options);
    }

    const key = JSON.stringify([locale || '', options]);
    let format = formats.get(key);
    if (!format) {
        format = new Intl.DateTimeFormat(locale, options);
        formats.set(key, format);
    }

    return format;
}

// What usable found, by zone name: a zone Intl knows does not stop being one
// while the page is open, nor does one it does not know start.
const zones = new Map<string, boolean>();

// Intl throws RangeError on a zone it does not recognise rather than falling
// back, and resolvedOptions().timeZone can come back empty on some engines. So
// every candidate is tried before it is trusted.
function usable(zone: string): boolean {
    if (!zone) {
        return false;
    }

    let known = zones.get(zone);
    if (known === undefined) {
        try {
            new Intl.DateTimeFormat('en', {timeZone: zone}).format(0);
            known = true;
        } catch {
            known = false;
        }
        zones.set(zone, known);
    }

    return known;
}

// The zone viewerZone read last, until the code running now has finished.
// The Clips list asks for it for every clip with no title, all in one
// render, and the machine's zone cannot change in the middle of that: a page
// hears of a new zone between one piece of work and the next.
let zoneJustRead: {zone: string | undefined} | null = null;

// viewerZone is the zone times are read in: the BROWSER's, always.
//
// It was briefly the Mattermost timezone preference when one was set, and that
// was wrong in a way worth recording. A datetime-local input is read and
// written by the browser in the browser's own zone — there is no way to hand
// the native picker a different one. So preferring another zone for DISPLAY
// while the form still worked in the browser's meant somebody with a manual
// Mattermost zone typed 20:00, saw "Saved as 01:45", and had no idea which of
// the two the stream would actually start at.
//
// One zone for both, and it has to be the one the picker uses. It is also the
// more truthful of the two: Mattermost's automatic zone is a copy taken
// whenever the webapp last got round to it, while the browser is right the
// moment somebody lands.
//
// undefined means "whatever this machine says", which is what Intl does with
// no timeZone at all — the correct fallback, not a failure.
//
// The machine's zone is read afresh for every render, with a formatter made
// to read it, so somebody who travels, or changes the zone in their system
// settings, is shown the new one at the next. Within one it is read once
// (zoneJustRead).
export function viewerZone(): string | undefined {
    if (zoneJustRead) {
        return zoneJustRead.zone;
    }

    let zone: string | undefined;
    try {
        const browser = Intl.DateTimeFormat().resolvedOptions().timeZone;
        if (usable(browser)) {
            zone = browser;
        }
    } catch {
        // Fall through: undefined.
    }

    zoneJustRead = {zone};
    void Promise.resolve().then(() => {
        zoneJustRead = null;
    });

    return zone;
}

function parts(at: number, zone: string | undefined, options: Intl.DateTimeFormatOptions): string {
    try {
        return formatter(undefined, {...options, timeZone: zone}).format(new Date(at));
    } catch {
        return formatter(undefined, options).format(new Date(at));
    }
}

// dayNumber is the calendar day AS SEEN IN THE TARGET ZONE, as a stable
// ordinal that can be subtracted.
//
// Two traps, both avoided here. It is not Math.floor(epoch / 86400000), which
// is a UTC day count and so is wrong in every zone whose offset is not a whole
// number of hours — Adelaide at +10:30, Kathmandu at +5:45. And "tomorrow" is
// not "now plus 86400000", which is an hour out for part of every clock-change
// day: on the evening before the clocks go forward, adding 24 hours lands on
// the day AFTER tomorrow, so a stream on Monday gets announced as "Tomorrow"
// on Saturday night.
//
// Reading the zone-local year/month/day and re-expressing it as a UTC midnight
// gives a number no clock change can shorten.
function fieldsOf(
    at: number,
    zone: string | undefined,
    options: Intl.DateTimeFormatOptions,
    locale?: string,
): Intl.DateTimeFormatPart[] {
    try {
        return formatter(locale, {...options, timeZone: zone}).formatToParts(new Date(at));
    } catch {
        return formatter(locale, options).formatToParts(new Date(at));
    }
}

function dayNumber(at: number, zone: string | undefined): number {
    const fields = fieldsOf(at, zone, {year: 'numeric', month: '2-digit', day: '2-digit'}, 'en');
    const value = (type: string) => Number((fields.find((part) => part.type === type) || {value: '0'}).value);

    return Date.UTC(value('year'), value('month') - 1, value('day')) / 86400000;
}

// clockTime is the time of day on its own: "20:00".
//
// hour12 is pinned off rather than left to the locale. This is a private server
// whose people read 24-hour time, and a card whose big line says "In 25 min"
// over a small line saying "8:00 PM" is two notations for one moment.
export function clockTime(at: number, zone: string | undefined): string {
    return parts(at, zone, {hour: '2-digit', minute: '2-digit', hour12: false});
}

// The same with its seconds: "21:02:17". Where a clip was cut from, which a
// two-minute stretch of a stream needs to the second to tell apart.
export function clockSeconds(at: number, zone: string | undefined): string {
    return parts(at, zone, {hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false});
}

// A day of the month, short: "31 Oct". When a clip goes, a month at most away.
export function dayMonth(at: number, zone: string | undefined): string {
    return parts(at, zone, {day: 'numeric', month: 'short'});
}

// dayWord names the calendar day: "Today", "Tomorrow", or "Sat 27 Sep".
export function dayWord(at: number, zone: string | undefined, now: number = Date.now()): string {
    const days = dayNumber(at, zone) - dayNumber(now, zone);

    if (days === 0) {
        return 'Today';
    }
    if (days === 1) {
        return 'Tomorrow';
    }

    return parts(at, zone, {weekday: 'short', day: 'numeric', month: 'short'});
}

// The day itself, never "Today": "Fri 17 Oct". What a shared plan's card
// says, since a post is read on other days than the one it was made.
export function dayDate(at: number, zone: string | undefined): string {
    return parts(at, zone, {weekday: 'short', day: 'numeric', month: 'short'});
}

// How far off a plan is, for the line under a shared plan's title: "in 40
// min", "in 3 hours", "tomorrow", "in 3 days" (design/3.2.0.md 6.7). Hours
// while it is today or under four hours away, as leadWhen's cutoff; after
// that the reader's calendar days, so a plan at 01:00 tomorrow read at 18:00
// is "tomorrow". The last half minute before it is "Due now", the word the
// card has once it has passed: 6.7 has no other.
export function planLede(startsAt: number, zone: string | undefined, now: number = Date.now()): string {
    const minutes = Math.round((startsAt - now) / 60000);
    if (minutes < 1) {
        return 'Due now';
    }
    if (minutes < 60) {
        return `in ${minutes} min`;
    }

    const days = dayNumber(startsAt, zone) - dayNumber(now, zone);
    if (days === 0 || minutes < 240) {
        const hours = Math.round(minutes / 60);

        return `in ${hours} ${hours === 1 ? 'hour' : 'hours'}`;
    }

    return days === 1 ? 'tomorrow' : `in ${days} days`;
}

// postedWhen is when a message was posted, short enough for a line of search
// results: minutes ago while it is that recent, then the day and the time
// while the day is this one or the one before, then just the date — with the
// year once it is not this year's. A clock a little ahead of the server's is
// "Just now" rather than a time in the future.
export function postedWhen(at: number, zone: string | undefined, now: number = Date.now()): string {
    const minutes = Math.floor((now - at) / 60000);
    if (minutes < 1) {
        return 'Just now';
    }
    if (minutes < 60) {
        return `${minutes} min ago`;
    }

    const days = dayNumber(now, zone) - dayNumber(at, zone);
    if (days === 0) {
        return `Today, ${clockTime(at, zone)}`;
    }
    if (days === 1) {
        return `Yesterday, ${clockTime(at, zone)}`;
    }

    const year = (instant: number) => fieldsOf(instant, zone, {year: 'numeric'}, 'en').find((part) => part.type === 'year')?.value;
    if (year(at) === year(now)) {
        return parts(at, zone, {weekday: 'short', day: 'numeric', month: 'short'});
    }

    return parts(at, zone, {day: 'numeric', month: 'short', year: 'numeric'});
}

// A day and a time, short, with no "today": "3 Oct 21:04". What a clip with
// no title of its own is called, after its streamer — it stays right however
// long after it is read.
export function shortStamp(at: number, zone: string | undefined): string {
    return `${parts(at, zone, {day: 'numeric', month: 'short'})} ${clockTime(at, zone)}`;
}

// The whole of it, for a tooltip: "Saturday 27 September 2025, 20:14".
export function postedAt(at: number, zone: string | undefined): string {
    return `${parts(at, zone, {weekday: 'long', day: 'numeric', month: 'long', year: 'numeric'})}, ${clockTime(at, zone)}`;
}

// formatWhen renders an instant the way somebody would say it out loud.
export function formatWhen(startsAt: number, zone: string | undefined, now: number = Date.now()): string {
    return `${dayWord(startsAt, zone, now)}, ${clockTime(startsAt, zone)}`;
}

// leadWhen is the big line on an Up next card: how soon, while that is the
// useful thing to know, and which day once it is not.
//
// The cutoff is four hours. Under it a countdown is something somebody can act
// on — finish this, be back by then. Over it nobody plans by counting hours;
// they plan by "Tomorrow" and a clock time, and the card's third line is
// already carrying the clock time. "In 11 hours" is arithmetic, not an answer.
//
// Nothing here returns an empty string. The lede is the whole point of the
// card, so every instant has to produce one.
export function leadWhen(startsAt: number, zone: string | undefined, now: number = Date.now()): string {
    const minutes = Math.round((startsAt - now) / 60000);

    if (minutes < 0) {
        return 'Started';
    }
    if (minutes < 1) {
        return 'Starting now';
    }
    if (minutes < 60) {
        return `In ${minutes} min`;
    }
    if (minutes < 240) {
        const hours = Math.round(minutes / 60);

        return `In ${hours} ${hours === 1 ? 'hour' : 'hours'}`;
    }

    return dayWord(startsAt, zone, now);
}

// instantAt turns a chosen day and a chosen time into the instant they mean,
// read in the browser's own zone — the one the buttons were labelled in.
//
// Built field by field through the Date constructor rather than by adding
// milliseconds to a midnight. A day containing a clock change is 23 or 25 hours
// long, so "midnight plus twenty hours" is an hour out for half the year.
//
// A time in a daylight-saving gap — 02:30 on the morning the clocks go forward
// — does not exist, and the constructor resolves it to the next real instant.
// That is why the form says back what will actually be saved instead of
// assuming it got what it asked for.
export function instantAt(day: Date, hour: number, minute: number): number {
    return new Date(day.getFullYear(), day.getMonth(), day.getDate(), hour, minute, 0, 0).getTime();
}
