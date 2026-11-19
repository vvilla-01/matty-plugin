import {fetchBoardsIfChanged} from './client';
import {now as clockNow} from './clock';
import {getSnapshot, subscribe as onSnapshot} from './store';
import type {Boards} from './types';

// The boards and everybody's record (contract/README.md, "Awards and the
// boards"), as the server last told this page: GET /api/v1/boards.
//
// They rode the snapshot until 3.6.0, and because a board's seconds tick for
// as long as anybody is on the air, the whole snapshot went to every
// connection once a minute to say so. Now they are read here, when somebody
// looks:
//
//   - once at the start, as the page starts, beside the first read of the
//     snapshot rather than after it, so the first profile opened has its
//     awards at once, and the panel its trophy and fan lines with its cards;
//   - when the snapshot's one word about them, boards.version, is not the
//     version of the copy in hand: a place, a medal, a fan or a step has
//     changed (a moment later, and not at the same moment as every other
//     open window);
//   - when a profile or the Trophies card opens, if the copy is a minute old;
//   - once a minute while the Trophies card stays open, since nothing says
//     that a second has passed on a board, and as its tab is looked at again
//     if the copy is a minute old by then.
//
// Every read sends the tag of the copy in hand, so most of them are answered
// "that one still". Nothing here decides anything about who holds what: what
// goes beside a name is in the snapshot, decided, and a profile's rows are in
// the boards, decided.
//
// Module-level, like store.ts and accounts.ts, because the panel, its header
// and every profile popover are separate React trees. No React here;
// useBoards.ts is the hook.

type Listener = () => void;

const listeners = new Set<Listener>();

const NOTHING: Boards = {version: '', people: {}};

let boards: Boards = NOTHING;

// The tag the copy in hand came under (the ETag of GET /api/v1/boards), sent
// back with the next read of it.
let boardsTag = '';

// When the server last vouched for the copy in hand, by the page's clock: 0
// for never.
let readAt = 0;

// The read on its way, which whoever asks meanwhile shares; and how many
// times the store has been emptied, so an answer already on its way cannot
// put back what was emptied.
let reading: Promise<void> | null = null;
let generation = 0;

// The version a read was last started for: one read per word the snapshot
// says, however many snapshots repeat it. And the word a read last missed,
// with when: a read that failed, or that came back with a copy from before
// the word was said, gives the word back, and the first snapshot to say it
// again half a minute later chases it again, none sooner. So a server that
// keeps failing is asked twice a minute at most, and one that comes back is
// caught up with, as it was when the boards came with every snapshot.
let chased = '';
let missed = '';
let missedAt = 0;
let followTimer = 0;
const BOARDS_AGAIN_MS = 30000;

// A copy this old is asked about again when somebody looks, and while they
// keep looking.
export const BOARDS_FRESH_MS = 60000;

// After the snapshot says the boards have changed, the read waits a random
// moment of up to this, so that the windows of everybody online do not all
// ask in the same instant.
export const BOARDS_SPREAD_MS = 3000;

export function getBoards(): Boards {
    return boards;
}

// Whether the server has ever answered: until it has, "nobody holds
// anything" is not yet known.
export function boardsRead(): boolean {
    return readAt !== 0;
}

export function onBoards(listener: Listener): () => void {
    listeners.add(listener);

    return () => {
        listeners.delete(listener);
    };
}

function notify(): void {
    Array.from(listeners).forEach((listener) => listener());
}

// Reads the boards again, saying which copy is in hand. One request at a
// time. A read that fails keeps what there is: the next look, or the next
// change, tries again.
export function loadBoards(): Promise<void> {
    if (reading) {
        return reading;
    }

    const asked = generation;
    const request: Promise<void> = fetchBoardsIfChanged(boards === NOTHING ? '' : boardsTag).then((fresh) => {
        if (asked !== generation) {
            return;
        }

        readAt = clockNow();
        if (fresh) {
            boards = {...fresh.value, version: fresh.value.version || '', people: fresh.value.people || {}};
            boardsTag = fresh.tag;
            notify();
        }
    }).catch(() => {
        // Kept as it was.
    }).then(() => {
        if (reading === request) {
            reading = null;
        }
    });
    reading = request;

    return request;
}

// Somebody is looking at a profile or a board: asked about again if the copy
// is a minute old, or was never read.
export function lookAtBoards(): void {
    if (readAt === 0 || clockNow() - readAt >= BOARDS_FRESH_MS) {
        void loadBoards();
    }
}

// ---- While a board stays open ----

let watchers = 0;
let watchTimer = 0;

// For an effect in something that shows a board for as long as it is open
// (the Trophies card): read now if the copy is old, and once a minute until
// it closes, but not while the page itself is hidden. Counted, so two of them
// never read twice.
//
// The minute's read asks whatever the copy's age: the timer is the schedule.
// Asked through lookAtBoards, it found the copy a few milliseconds short of a
// minute old every time (the answer lands a moment after the timer starts)
// and read only every other minute.
export function watchBoards(): () => void {
    watchers++;
    lookAtBoards();
    if (watchers === 1) {
        watchTimer = window.setInterval(() => {
            if (typeof document === 'undefined' || document.visibilityState !== 'hidden') {
                void loadBoards();
            }
        }, BOARDS_FRESH_MS);
        if (typeof document !== 'undefined') {
            document.addEventListener('visibilitychange', lookedAtAgain);
        }
    }

    let released = false;

    return () => {
        if (released) {
            return;
        }
        released = true;

        watchers--;
        if (watchers === 0) {
            window.clearInterval(watchTimer);
            watchTimer = 0;
            if (typeof document !== 'undefined') {
                document.removeEventListener('visibilitychange', lookedAtAgain);
            }
        }
    };
}

// A board left open in a tab that was hidden is read as the tab is looked at
// again, if the copy is a minute old by then: the minute's read skips a
// hidden page.
function lookedAtAgain(): void {
    if (document.visibilityState !== 'hidden') {
        lookAtBoards();
    }
}

// ---- Following the snapshot's word ----

// The snapshot as it now stands: when its word for the boards is not the
// version in hand, they are read again, once for that word. The first read,
// with nothing in hand, goes at once; a later one a random moment later.
function followSnapshot(): void {
    const wanted = getSnapshot().boards?.version || '';
    if (wanted === '' || wanted === boards.version || wanted === chased) {
        return;
    }
    if (wanted === missed && clockNow() - missedAt < BOARDS_AGAIN_MS) {
        return;
    }
    chased = wanted;

    window.clearTimeout(followTimer);
    followTimer = 0;
    if (readAt === 0) {
        chase(wanted);

        return;
    }

    followTimer = window.setTimeout(() => {
        followTimer = 0;
        chase(wanted);
    }, Math.random() * BOARDS_SPREAD_MS);
}

// The read for a word the snapshot has said. A read already on its way may
// have left before that word existed: once it lands, the boards are read
// again at once if the copy is still behind. A read that fails, or comes
// back still behind (another node of a cluster had not caught up), gives the
// word back to be chased again (followSnapshot).
function chase(wanted: string): void {
    const asked = generation;
    const behind = () => asked === generation && chased === wanted && boards.version !== wanted;

    const inFlight = reading;
    const read = inFlight ? inFlight.then(() => (behind() ? loadBoards() : undefined)) : loadBoards();
    void read.then(() => {
        if (behind()) {
            chased = '';
            missed = wanted;
            missedAt = clockNow();
        }
    });
}

// Wires the store to the snapshot for one window; returns what undoes it,
// which also empties the store. The first read goes at once, as the page
// starts, beside the first read of the snapshot: the snapshot saying the
// same version meanwhile waits for this read rather than making another.
export function installBoards(): () => void {
    const unfollow = onSnapshot(followSnapshot);
    void loadBoards();
    followSnapshot();

    return () => {
        unfollow();
        window.clearTimeout(followTimer);
        followTimer = 0;
        generation++;
        reading = null;
        boards = NOTHING;
        boardsTag = '';
        readAt = 0;
        chased = '';
        missed = '';
        missedAt = 0;
        notify();
    };
}
