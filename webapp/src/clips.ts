import React from 'react';

import {windowKind} from './away';
import {cardNeedsFetch, cardRefreshDelay, heldTails, isDownloadAddress, nextTailEnd, renewBeforeRefusing, votedNow} from './clipLogic';
import type {CardEntry, CardFetch} from './clipLogic';
import {
    ClipRequestError,
    checkDraftVideo,
    createDraft,
    deleteClip,
    deleteDraft,
    fetchClip,
    fetchClipDownload,
    fetchClipCards,
    fetchClips,
    fetchDrafts,
    publishDraft,
    reportClip,
    saveClipFrom,
    setClipDownloads,
    setClipsAllowed,
    shareClip,
    uploadDraftThumb,
    voteClip,
    wipeRecording,
} from './client';
import {accessNow, getIdentity, refreshIdentity} from './identity';
import {isLinked, mayWatch} from './lockLogic';
import {getPending, onPendingChange, openLock} from './lockPending';
import {PLUGIN_BASE} from './manifest';
import {currentChannelId, currentUserId} from './mattermost';
import {getSessionInfo, renewSession} from './sessions';
import {getSnapshot, subscribe as subscribeToSnapshot} from './store';
import {shortStamp, viewerZone} from './time';
import type {Clip, ClipCard, ClipDraft, ClipErrorCode, ClipPerson, ClipSource, ClipTail, ClipsInfo} from './types';
import {markOpenedByHand, openPanel} from './watching';

// Clips, for everything in the page that shows them: whether the Clips
// dialog is open and on what, the list it shows, the reader's drafts, and
// what they had already seen. Module-level, like store.ts and watching.ts,
// because the dialog is hosted at the root of the app (ClipsRoot) while
// what opens it — the header's clapper, the panel's row, the trophies, a
// shared clip's card in a channel — is in other React trees altogether.
//
// Watching clips is for people who have linked their stream and for people a
// system admin let in; everything else about them — making, upvoting,
// reporting, sharing, drafts — is for linked people alone (design/lock-
// screen.md, which amends Victor's Clips rule for viewing only). The server
// refuses everybody else. So nothing is asked for that the reader may not
// have; what was fetched is dropped the moment they may no longer have it
// (access.ts); and any refusal saying so has "me" asked for again, so every
// surface follows at once (refreshIdentity).

type Listener = () => void;

const listeners = new Set<Listener>();

function notify(): void {
    Array.from(listeners).forEach((listener) => listener());
}

function useObservable<T>(read: () => T): T {
    const [, forceRender] = React.useState(0);

    React.useEffect(() => {
        const listener = () => forceRender((value) => value + 1);
        listeners.add(listener);

        return () => {
            listeners.delete(listener);
        };
    }, []);

    return read();
}

// Linked, as the server last said in "me" (identity.ts accessNow): the
// server decides again on every request. False too while "me" is not known,
// so nothing is asked for, and while Mattermost says somebody else is signed
// in, or nobody: a "me" left over from the person before is not this
// person's (index.tsx forgets it as they sign out; this is for the moment in
// between). For making, drafts, publishing and anything else that acts.
function linkedNow(): boolean {
    return isLinked(accessNow());
}

// Linked or let in: for listing, opening and watching clips, and shared
// clips' cards with their pictures.
function mayWatchClipsNow(): boolean {
    return mayWatch(accessNow());
}

// Whether a refusal says the reader may not have what they asked for: locked,
// let in where a link is needed, or (from a server before the lock) not
// linked.
export function isAccessRefusal(code: string): boolean {
    return code === 'locked' || code === 'watch_only' || code === 'not_linked';
}

// The code a failed clip request came back with: 'network' for anything
// that is not one of the client's refusals.
export function clipErrorCodeOf(reason: unknown): ClipErrorCode {
    return reason instanceof ClipRequestError ? reason.code : 'network';
}

// The same, for the requests made here, which also act on it: a refusal
// saying the reader may not do that has "me" fetched again — once, however
// many say it at once — so every surface follows. Components only ever read
// the code; the request that heard it has already done this. (client.ts tells
// the page too; the two share one request.)
function refusal(reason: unknown): ClipErrorCode {
    const code = clipErrorCodeOf(reason);
    if (isAccessRefusal(code)) {
        refreshIdentity();
    }

    return code;
}

// The server's own words for a refusal, for a code this build has none for:
// only what the plugin's server wrote, never the client's placeholder for an
// answer that carried none (Mattermost's own 401, a proxy's 502), so each
// surface's own "could not … just now" shows for those.
export function clipErrorTextOf(reason: unknown): string {
    return reason instanceof ClipRequestError ? reason.serverText : '';
}

// ---- The editor ----

// The editor a press of the scissors opens (spec 3.6), hosted beside the
// dialog by ClipsRoot. Its state is here, not in the component, so that the
// scissors can say "Clipping…" while it cuts, so that it outlives the panel
// (Mattermost unmounts the side panel; the editor is at the root), and so
// that what was asked of the server is answered to the editor that asked:
// every cut, publish and look-up carries the editor's ticket, and an answer
// for an editor that has since closed, or cut again, only refreshes the
// drafts.
export type EditorPhase = 'cutting' | 'ready' | 'publishing' | 'published' | 'error';

export type EditorState = null | {
    handle: string;
    source: ClipSource;
    streamName: string;
    lengthSeconds: number;
    phase: EditorPhase;
    draft?: ClipDraft;
    clip?: Clip;
    error?: ClipErrorCode;
    returnFocus?: HTMLElement | null;

    // The server's own words for a refusal, for a code this build has no
    // copy of.
    errorText?: string;

    // Which step the error is about: cutting the draft (there is none), its
    // file (it has gone: expired, or wiped by its streamer), or publishing it
    // (it is still there, and can be tried again).
    failed?: 'capture' | 'draft' | 'publish';

    // The publish's answer was lost, or the server said one is already
    // running: the clip is being looked for before Try again is offered
    // (spec 3.6, "Publish after a network error").
    checking?: boolean;

    // The end of a stream that went off or dropped, rather than a live one,
    // for the line saying where the draft is from.
    tail?: 'ended' | 'dropped';

    // Opened from the dialog's drafts, which closing it goes back to.
    fromList?: boolean;
};

let editor: EditorState = null;

// Which editor an answer is for. Bumped whenever the editor opens, closes or
// cuts again; a publish and its look-ups keep the one they started with.
let editorTicket = 0;

export const getClipEditor = (): EditorState => editor;

// How many times the editor has been opened, for ClipsRoot's error boundary,
// as the dialog's openings are: an editor that failed once is tried afresh.
let editorOpenings = 0;

export const getEditorOpenings = (): number => editorOpenings;

export const useClipEditor = (): EditorState => useObservable(getClipEditor);

function setEditor(next: EditorState): void {
    editor = next;
    notify();
}

// The editor as it is now, changed, if it is still the one that asked.
function updateEditor(ticket: number, fields: Partial<NonNullable<EditorState>>): void {
    if (ticket === editorTicket && editor !== null) {
        setEditor({...editor, ...fields});
    }
}

// What was typed and trimmed for each draft, kept in memory only, so Later
// and then Edit come back to it, and so does an editor that the page had to
// mount again. Never sent anywhere but the publish itself; forgotten with
// everything else when the reader unlinks or somebody else signs in.
export type DraftEdits = {title: string; inMs: number; outMs: number};

const edits = new Map<string, DraftEdits>();

export const editsOf = (id: string): DraftEdits | undefined => edits.get(id);

export function keepEdits(id: string, value: DraftEdits): void {
    edits.set(id, value);
}

// Why the stream a draft was cut from the end of had stopped — it went off, or
// it dropped — for the editor's line saying where the draft is from, which
// ends "before it went off". The server says so on the draft itself
// (draft.tailReason), which is what is read first: it is there after a
// reload, and long after the end it was cut from has been let go (a tail is
// held for minutes, a draft for two hours). The reason known when the scissors
// were pressed is kept here as well, in memory, for a draft from a server that
// did not say. Forgotten with the edits.
//
// Never guessed from whichever end the snapshot holds for the same handle: the
// same stream can have dropped again since, and that end's reason is not this
// draft's.
const tailReasons = new Map<string, 'ended' | 'dropped'>();

function tailReasonOf(draft: ClipDraft): 'ended' | 'dropped' | undefined {
    if (draft.source !== 'tail') {
        return undefined;
    }

    return draft.tailReason || tailReasons.get(draft.id);
}

// Asks the server to cut the draft, for the editor as it is now.
function cut(): void {
    const ticket = ++editorTicket;
    const state = editor;
    if (state === null) {
        return;
    }

    setEditor({...state, phase: 'cutting', error: undefined, errorText: undefined, failed: undefined, checking: false});

    // Victor's rule lets anybody linked who is watching the stream clip it,
    // and the server knows who is watching only from the exchange that
    // started their tile, in memory. Refused as not watching while this
    // window's tile for it is plainly playing: the server has forgotten them
    // (it restarted, or Clips was switched on after the tile started). The
    // tile is tuned in again, which the server hears, and the cut asked for
    // once more, still as "cutting", before any refusal is shown. Once: a
    // second not_watching is the server's real answer.
    let renewed = false;
    const asker = getIdentity()?.userId || '';
    const ask = () => {
        createDraft(state.handle, state.source).then((draft) => {
            // The server's word for why the stream had stopped, over the
            // scissors' own, which came from the snapshot.
            const tail = state.source === 'tail' ? draft.tailReason || state.tail : undefined;
            if (tail) {
                tailReasons.set(draft.id, tail);
            }

            // Made, whether or not anybody is still waiting for it: the
            // header's count and the dialog's drafts show it either way, so a
            // press of Later while it was cutting still keeps it.
            refreshDrafts();
            updateEditor(ticket, {phase: 'ready', draft, ...(tail ? {tail} : {})});
        }, (reason: unknown) => {
            const code = refusal(reason);
            if (renewBeforeRefusing(state.source, code, getSessionInfo(state.handle)?.status || '', renewed)) {
                renewed = true;

                // Asked again even if the editor has closed meanwhile, as the
                // first answer would have been kept: Later while cutting keeps
                // the draft.
                renewSession(state.handle).then((playing) => {
                    // Not for whoever is signed in now if that is somebody
                    // else, or the reader is no longer linked.
                    if (playing && linkedNow() && getIdentity()?.userId === asker) {
                        ask();
                    } else {
                        updateEditor(ticket, {phase: 'error', failed: 'capture', error: code, errorText: clipErrorTextOf(reason)});
                    }
                });

                return;
            }

            updateEditor(ticket, {phase: 'error', failed: 'capture', error: code, errorText: clipErrorTextOf(reason)});
        });
    };
    ask();
}

// One press of the scissors, or "Clip the end" in the dialog: the editor
// opens at once, cutting, with the title ready to type into, while the
// server cuts the last few minutes of the stream into a draft. One editor at
// a time; a press while one is open is not possible from the page, and does
// nothing from anywhere else.
export function startClip(stream: {handle: string; title: string}, source: ClipSource, lengthSeconds: number,
    returnFocus: HTMLElement | null, tail?: 'ended' | 'dropped'): void {
    if (editor !== null || !linkedNow()) {
        return;
    }

    const fromList = dialog !== null;
    if (fromList) {
        closeClips();
    }

    editorOpenings++;
    editor = {
        handle: stream.handle,
        source,
        streamName: stream.title,
        lengthSeconds,
        phase: 'cutting',
        returnFocus,
        ...(tail ? {tail} : {}),
        ...(fromList ? {fromList} : {}),
    };
    cut();
}

// Try again, after the cut was refused or failed.
export function retryCapture(): void {
    if (editor?.phase === 'error' && editor.failed === 'capture' && linkedNow()) {
        cut();
    }
}

// "Edit" on one of the reader's drafts, in the dialog: the editor takes the
// dialog's place, and closing it goes back there.
export function editDraft(draft: ClipDraft): void {
    if (editor !== null || !linkedNow() || draft.state !== 'ready') {
        return;
    }

    const fromList = dialog !== null;
    if (fromList) {
        closeClips();
    }

    editorTicket++;
    editorOpenings++;
    const tail = tailReasonOf(draft);
    setEditor({
        handle: draft.handle,
        source: draft.source,
        streamName: draft.streamer.name,
        lengthSeconds: Math.round(draft.requestedMs / 1000),
        phase: 'ready',
        draft,
        returnFocus: null,
        ...(tail ? {tail} : {}),
        ...(fromList ? {fromList} : {}),
    });
}

// Closing it keeps the draft, as Later does. An editor opened from the
// dialog's drafts goes back to them, unless whatever closes it is opening
// something else (Watch it, Link your stream).
export function closeEditor(backToList = true): void {
    if (editor === null) {
        return;
    }

    const back = backToList && editor.fromList;
    const draftId = editor.draft?.id || '';
    const tailHandle = editor.source === 'tail' ? editor.handle : '';
    editorTicket++;
    stopChecking();
    setEditor(null);
    if (back) {
        draftToRefocus = draftId;
        tailToRefocus = tailHandle;
        openClips();
    }
}

// The draft whose Edit opened the editor, for the dialog it closes back into
// to put focus on that Edit again, while the draft is still there. Read once.
let draftToRefocus = '';

export function takeDraftToRefocus(): string {
    const id = draftToRefocus;
    draftToRefocus = '';

    return id;
}

// And the stream whose end the editor was cutting, for an editor that made
// no draft — the cut was refused, or failed — to go back to the Just ended
// row whose button opened it, while that end is still held, rather than to
// the dialog's Close. Read once, as the draft is.
let tailToRefocus = '';

export function takeTailToRefocus(): string {
    const handle = tailToRefocus;
    tailToRefocus = '';

    return handle;
}

// ---- Publishing ----

const CHECK_EVERY_MS = 2000;
const CHECK_FOR_MS = 60000;
let checkTimer = 0;

function stopChecking(): void {
    window.clearTimeout(checkTimer);
    checkTimer = 0;
}

function published(ticket: number, clip: Clip): void {
    edits.delete(clip.id);

    // Known at once, so Watch it opens it without a wait.
    byId.set(clip.id, clip);
    changed();
    updateEditor(ticket, {phase: 'published', clip, error: undefined, errorText: undefined, failed: undefined, checking: false});
}

// A publish whose answer was lost may have worked, and one the server says is
// already running will finish: either way the clip is looked for — its id is
// the draft's — every two seconds for a minute, before Try again is offered.
// A retry is safe whatever happened: the server answers it with the clip, or
// says it is still publishing.
function lookForClip(ticket: number, id: string): void {
    stopChecking();
    const until = Date.now() + CHECK_FOR_MS;
    const look = () => {
        checkTimer = 0;
        if (ticket !== editorTicket) {
            return;
        }

        // Unlinked since the last look: nothing more is asked (addendum 3.8,
        // no clip request while not linked). The editor says why, and a
        // relink brings it back to PUBLISH, which the server answers with
        // the clip if this publish went through after all.
        if (!linkedNow()) {
            stoppedLooking();

            return;
        }

        fetchClip(id).then((clip) => {
            if (ticket === editorTicket) {
                published(ticket, clip);
                refreshDrafts();
            }
        }, (reason: unknown) => {
            if (ticket !== editorTicket) {
                return;
            }

            const code = refusal(reason);
            if (isAccessRefusal(code)) {
                updateEditor(ticket, {phase: 'error', failed: 'publish', error: code, checking: false});
            } else if (code === 'clip_gone' || Date.now() >= until) {
                // Not published after all, or no answer in a minute.
                updateEditor(ticket, {phase: 'error', failed: 'publish', error: 'store', errorText: '', checking: false});
            } else {
                checkTimer = window.setTimeout(look, CHECK_EVERY_MS);
            }
        });
    };

    checkTimer = window.setTimeout(look, CHECK_EVERY_MS);
}

// A publish that was being looked for, no longer looked for because the
// reader is not linked: the editor locks on watch_only, which editorRelinked
// undoes. The ticket moves on, so a look already on its way is answered to
// nobody.
function stoppedLooking(): void {
    stopChecking();
    if (editor?.checking) {
        editorTicket++;
        setEditor({...editor, phase: 'error', failed: 'publish', error: 'watch_only', errorText: undefined, checking: false});
    }
}

// What a step of a publish is refused with when the reader is not linked by
// the time it would be sent: it is not sent, and the editor says why.
const notLinkedNow = (): ClipRequestError => new ClipRequestError(403, 'watch_only');

// PUBLISH (spec 3.6): the picture first, taken by the editor from the frame
// half a second in and sent as the draft's thumbnail — skipped without a word
// if it cannot be taken or sent — then the publish, then the drafts again.
// thumbnail resolves to the JPEG, or null for none.
export function publishEditing(chosen: DraftEdits, thumbnail: () => Promise<Blob | null>): void {
    const state = editor;
    const draft = state?.draft;
    if (!state || !draft || state.phase === 'publishing' || state.checking || !linkedNow()) {
        return;
    }

    const ticket = editorTicket;
    keepEdits(draft.id, chosen);
    setEditor({...state, phase: 'publishing', error: undefined, errorText: undefined, failed: undefined, checking: false});

    // Each step is sent only while the reader is still linked, and a refusal
    // of the picture saying they are not stops the publish there: the
    // publish would be refused too, and is a clip request nobody unlinked
    // makes (addendum 3.8).
    thumbnail().catch(() => null).then((jpeg) => {
        if (!jpeg) {
            return undefined;
        }
        if (!linkedNow()) {
            throw notLinkedNow();
        }

        return uploadDraftThumb(draft.id, jpeg).catch((reason: unknown) => {
            // Published without one, unless the reader is not linked.
            if (isAccessRefusal(clipErrorCodeOf(reason))) {
                throw reason;
            }
        });
    }).then(() => {
        if (!linkedNow()) {
            throw notLinkedNow();
        }

        return publishDraft(draft.id, chosen);
    }).then((clip) => {
        refreshDrafts();
        published(ticket, clip);
    }, (reason: unknown) => {
        refreshDrafts();
        const code = refusal(reason);
        if (code === 'network' || code === 'publishing') {
            updateEditor(ticket, {phase: 'error', failed: 'publish', error: code, errorText: '', checking: true});
            lookForClip(ticket, draft.id);

            return;
        }

        updateEditor(ticket, {
            phase: 'error',
            failed: code === 'draft_gone' || code === 'draft_wiped' ? 'draft' : 'publish',
            error: code,
            errorText: clipErrorTextOf(reason),
        });
    });
}

// Discard, on its second press: the draft deleted on the server, and the
// editor closed. Rejects with the refusal, for the editor to say.
export function discardEditing(): Promise<void> {
    const draft = editor?.draft;
    if (!draft) {
        closeEditor();

        return Promise.resolve();
    }

    return discardDraft(draft.id).then(() => {
        edits.delete(draft.id);
        if (editor?.draft?.id === draft.id) {
            closeEditor();
        }
    });
}

// The editor's <video> would not load its draft: whether the draft is still
// there to be had. 'ok' when it is, which leaves the browser to blame; 'gone'
// when it expired or its streamer wiped it, which the editor then says;
// 'failed' when the server could not be asked, or the reader is not linked
// any more (the editor locks by itself).
export function checkEditingDraft(): Promise<'ok' | 'gone' | 'failed'> {
    const draft = editor?.draft;
    if (!draft) {
        return Promise.resolve('failed');
    }

    const ticket = editorTicket;

    return checkDraftVideo(draft.id).then(() => 'ok' as const, (reason: unknown) => {
        const code = refusal(reason);
        if (code !== 'draft_gone' && code !== 'draft_wiped') {
            return 'failed' as const;
        }

        refreshDrafts();

        // Only while the draft is still what the editor is about. Once a
        // publish has started, the draft's file goes as the clip is made
        // (the server moves it), so a check that lands then says "gone" of a
        // draft that has just become a clip: the publish's own answer says
        // what happened.
        if (editor?.phase === 'ready' || (editor?.phase === 'error' && !editor.checking)) {
            updateEditor(ticket, {phase: 'error', failed: 'draft', error: code, errorText: clipErrorTextOf(reason), checking: false});
        }

        return 'gone' as const;
    });
}

// The reader linked again while the editor said they were not: what they
// were doing goes on. An editor that had not got as far as a draft closes;
// they press the scissors again.
export function editorRelinked(): void {
    if (!editor?.error || !isAccessRefusal(editor.error)) {
        return;
    }

    if (editor.draft) {
        editorTicket++;
        setEditor({...editor, phase: 'ready', error: undefined, errorText: undefined, failed: undefined, checking: false});
    } else {
        closeEditor(false);
    }
}

// ---- The dialog ----

export type ClipFilter = 'all' | 'yours';

// What the Clips dialog shows: the list, or one clip. 'locked' is a clip
// asked for while the reader could not watch it (a shared clip's Watch, a
// clip link, while the panel was locked): the dialog is not drawn at all
// until the lock opens, and then it is that clip (access.ts). direct means
// the clip was asked for
// by itself (the trophies, the editor, a link), so Escape closes rather than
// going back to a list nobody asked for. filter is the list's, kept while a
// clip is open so Back returns to the same list.
export type DialogState = null
    | {view: 'list'; filter?: ClipFilter}
    | {view: 'clip'; id: string; direct: boolean; filter?: ClipFilter}
    | {view: 'locked'; clipId?: string};

let dialog: DialogState = null;

// How many times the dialog has been opened from closed. ClipsRoot keys its
// error boundary on it, so a dialog that failed once is tried afresh the next
// time somebody opens it rather than staying blank until a reload.
let openings = 0;

export const getClipsDialog = (): DialogState => dialog;

export const useClipsDialog = (): DialogState => useObservable(getClipsDialog);

export const getClipsOpenings = (): number => openings;

function setDialog(next: DialogState): void {
    if (dialog === null && next !== null) {
        openings++;
    }
    dialog = next;
    notify();
}

// Opens the dialog: on one clip when given its id, otherwise on the list,
// all of it or only the reader's own.
export function openClips(id?: string, filter?: ClipFilter): void {
    setDialog(id ? {view: 'clip', id, direct: true} : {view: 'list', filter: filter || 'all'});
}

export function closeClips(): void {
    if (dialog !== null) {
        setDialog(null);
    }
}

// From the list to one of its clips, and back again to the same list.
export function showClip(id: string): void {
    const filter = dialog?.view === 'list' ? dialog.filter : undefined;
    setDialog({view: 'clip', id, direct: false, filter});
}

export function backToList(): void {
    const filter = dialog?.view === 'clip' ? dialog.filter : undefined;
    setDialog({view: 'list', filter: filter || 'all'});
}

export function setListFilter(filter: ClipFilter): void {
    if (dialog?.view === 'list') {
        setDialog({view: 'list', filter});
    }
}

// A clip asked for while the lock was shown, no longer wanted: something else
// was asked for since, or the panel was opened from its own button, which
// asks for the panel and not for that clip (lockPending.ts). One whose wait
// simply ran out is let go when the lock opens (access.ts).
onPendingChange(() => {
    if (dialog?.view !== 'locked') {
        return;
    }

    const item = getPending();
    if (!item || item.kind !== 'clip' || item.clipId !== dialog.clipId) {
        setDialog(null);
    }
});

// The lock has opened: a clip that was waiting for it is opened, and a
// waiting dialog whose clip is no longer wanted is closed.
export function openWaitingClip(clipId: string | null): void {
    if (clipId) {
        setDialog({view: 'clip', id: clipId, direct: true});
    } else if (dialog?.view === 'locked') {
        setDialog(null);
    }
}

// ---- The list ----

export type ClipList = {
    status: 'idle' | 'loading' | 'ready' | 'error';

    // In the server's order; the dialog sorts them itself.
    ids: string[];
    error?: ClipErrorCode;
};

let list: ClipList = {status: 'idle', ids: []};

// Every clip known, by id: the list's, and one opened by itself that the list
// may not have yet. The list and the viewer both read from here, so an upvote
// in one shows in the other.
let byId = new Map<string, Clip>();

// Which request is the newest, per kind, and how many times everything has
// been dropped. An answer to an older request, or to one made before the
// reader unlinked, is dropped too.
let listTicket = 0;
let draftsTicket = 0;
let generation = 0;

// A version of everything above, for hooks that compare by value.
let revision = 0;

function changed(): void {
    revision++;
    notify();
}

export const getClipList = (): ClipList => list;

export const getClip = (id: string): Clip | undefined => byId.get(id);

// The list, re-rendered whenever any clip in it changes.
export function useClipList(): ClipList {
    useObservable(() => revision);

    return list;
}

export function useClip(id: string): Clip | undefined {
    useObservable(() => revision);

    return byId.get(id);
}

// Upvotes asked for and not yet answered, by clip: the newest request, and
// the clip's count and vote as the server last said them, to go back to if
// it fails. A list that arrives meanwhile does not undo what was pressed.
const pendingVotes = new Map<string, {ticket: number; settled: {upvotes: number; voted: boolean}}>();
let voteTicket = 0;

function keepPending(clip: Clip): Clip {
    const pending = pendingVotes.get(clip.id);
    const shown = byId.get(clip.id);
    if (!pending || !shown) {
        return clip;
    }

    pending.settled = {upvotes: clip.upvotes, voted: clip.voted};

    return {...clip, upvotes: shown.upvotes, voted: shown.voted};
}

// Fetches the list, while the reader is linked. A list already on screen stays
// there while a newer one is fetched, and stays if that fails: only the first
// fetch shows the loading cards, and only its failure the error.
export function loadClips(): Promise<void> {
    if (!mayWatchClipsNow()) {
        return Promise.resolve();
    }

    const ticket = ++listTicket;
    if (list.status !== 'ready') {
        list = {status: 'loading', ids: []};
        changed();
    }

    return fetchClips().then((answer) => {
        if (ticket !== listTicket) {
            return;
        }

        const clips = Array.isArray(answer?.clips) ? answer.clips : [];
        const next = new Map<string, Clip>();
        clips.forEach((clip) => next.set(clip.id, keepPending(clip)));

        // The clip open in the viewer stays known even if this list, asked
        // for before it was published, does not have it: the viewer asks
        // about it by itself.
        const open = dialog?.view === 'clip' ? byId.get(dialog.id) : undefined;
        if (open && !next.has(open.id)) {
            next.set(open.id, open);
        }

        byId = next;
        list = {status: 'ready', ids: clips.map((clip) => clip.id)};
        changed();
    }).catch((reason: unknown) => {
        if (ticket !== listTicket) {
            return;
        }

        const code = refusal(reason);
        if (list.status !== 'ready') {
            list = {status: 'error', ids: [], error: code};
            changed();
        }
    });
}

// One clip, fetched by itself for the viewer: the clip, or null when the
// answer came after everything was dropped (the reader unlinked meanwhile),
// which says nothing about the clip. Rejects with the refusal, and forgets a
// clip the server says has gone.
export function loadClip(id: string): Promise<Clip | null> {
    if (!mayWatchClipsNow()) {
        return Promise.resolve(null);
    }

    const asked = generation;

    return fetchClip(id).then((clip) => {
        if (asked !== generation) {
            return null;
        }

        byId.set(id, keepPending(clip));
        changed();

        return clip;
    }).catch((reason: unknown) => {
        if (refusal(reason) === 'clip_gone' && asked === generation) {
            forget(id);
        }

        throw reason;
    });
}

function forget(id: string): void {
    if (!byId.has(id) && !list.ids.includes(id)) {
        return;
    }

    byId.delete(id);
    list = {...list, ids: list.ids.filter((candidate) => candidate !== id)};
    changed();
}

function patch(id: string, fields: Partial<Clip>): void {
    const clip = byId.get(id);
    if (!clip) {
        return;
    }

    byId.set(id, {...clip, ...fields});
    changed();
}

// An upvote, or taking one back: shown at once, then put right by the
// server's answer, or put back if it fails. up is the state wanted, never a
// toggle, so pressing twice quickly ends where the second press asked, and
// only the newest press's answer is shown.
export function voteOn(id: string, up: boolean): Promise<void> {
    const clip = byId.get(id);
    if (!clip) {
        return Promise.resolve();
    }

    const ticket = ++voteTicket;
    const settled = pendingVotes.get(id)?.settled || {upvotes: clip.upvotes, voted: clip.voted};
    pendingVotes.set(id, {ticket, settled});
    if (clip.voted !== up) {
        patchVotes(id, votedNow(clip, up));
    }

    return voteClip(id, up).then((answer) => {
        const pending = pendingVotes.get(id);
        if (!pending) {
            return;
        }
        if (pending.ticket !== ticket) {
            // A newer press is on its way; this is now what it falls back to.
            pending.settled = {upvotes: answer.upvotes, voted: answer.voted};

            return;
        }

        pendingVotes.delete(id);
        patchVotes(id, {upvotes: answer.upvotes, voted: answer.voted});
    }).catch((reason: unknown) => {
        const code = refusal(reason);
        const pending = pendingVotes.get(id);
        if (pending && pending.ticket === ticket) {
            pendingVotes.delete(id);
            patchVotes(id, pending.settled);
        }
        if (code === 'clip_gone') {
            forget(id);
        }

        throw reason;
    });
}

// A vote's count and mark, on both copies of the clip a page can hold: the
// Clips dialog's, and a shared post's card's (design/3.2.0.md 9.3). One place
// for it, so the two cannot disagree.
function patchVotes(id: string, votes: {upvotes: number; voted: boolean}): void {
    patch(id, votes);
    patchSharedCard(id, votes);
}

// A thumbs up from a shared clip's card: shown on the card at once, then put
// right by the server's answer, or put back if it fails, as voteOn does in the
// dialog. Through voteOn when the dialog holds the clip too, so both copies
// move together, and a vote in the dialog moves the card the same way.
//
// A clip that has gone since the card was drawn is forgotten by the dialog
// (voteOn) and turns the card to its gone face, so it stops offering a thumb
// that can only be refused again.
export function voteOnCard(id: string, up: boolean): Promise<void> {
    if (byId.has(id)) {
        return voteOn(id, up).catch((reason: unknown) => {
            if (clipErrorCodeOf(reason) === 'clip_gone') {
                sharedCardGone(id);
            }

            throw reason;
        });
    }

    const card = sharedCards.get(id)?.card;
    if (!card || typeof card.upvotes !== 'number') {
        return Promise.resolve();
    }

    const before = {upvotes: card.upvotes, voted: card.voted === true};
    patchSharedCard(id, votedNow(before, up));

    const asked = cardsGeneration;

    return voteClip(id, up).then((answer) => {
        if (asked === cardsGeneration) {
            patchVotes(id, {upvotes: answer.upvotes, voted: answer.voted});
        }
    }).catch((reason: unknown) => {
        const code = refusal(reason);
        if (asked === cardsGeneration) {
            patchSharedCard(id, before);
            if (code === 'clip_gone') {
                sharedCardGone(id);
            }
        }

        throw reason;
    });
}

export function reportOn(id: string): Promise<void> {
    return reportClip(id).then(() => {
        patch(id, {reported: true});
    }).catch((reason: unknown) => {
        if (refusal(reason) === 'clip_gone') {
            forget(id);
        }

        throw reason;
    });
}

// Downloading a clip (design/3.2.1.md 1.4): a ticket from the server, then the
// browser saves the file from the address it answered, never through the
// page. An address that is not the plugin's own download route is not
// opened. A clip that has gone is forgotten, as for a report.
export function downloadClip(id: string): Promise<void> {
    return fetchClipDownload(id).then((answer) => {
        if (!isDownloadAddress(answer?.url, PLUGIN_BASE)) {
            throw new ClipRequestError(0, 'store');
        }

        saveClipFrom(answer.url);
    }).catch((reason: unknown) => {
        if (refusal(reason) === 'clip_gone') {
            forget(id);
        }

        throw reason;
    });
}

// Deleting a clip. Gone already counts as done, as the server answers it.
export function deleteOn(id: string): Promise<void> {
    return deleteClip(id).then(() => {
        forget(id);
        loadClips();
    }).catch((reason: unknown) => {
        if (refusal(reason) === 'clip_gone') {
            forget(id);

            return;
        }

        throw reason;
    });
}

// ---- Drafts ----

let drafts: ClipDraft[] = [];

export const getDrafts = (): ClipDraft[] => drafts;

export const useDrafts = (): ClipDraft[] => useObservable(getDrafts);

// The drafts still waiting for their owner: not published and not yet gone.
// What the header's count and the panel's row say there are.
export function waitingDrafts(all: ClipDraft[], now: number): ClipDraft[] {
    return all.filter((draft) => draft.state !== 'published' && draft.expiresAt > now);
}

// The reader's drafts, asked for again: when the host mounts, when the
// dialog opens, when the reader links, and after a draft is made or let go.
// Never while they are not linked.
export function refreshDrafts(): Promise<void> {
    if (!linkedNow()) {
        return Promise.resolve();
    }

    const ticket = ++draftsTicket;

    return fetchDrafts().then((answer) => {
        if (ticket !== draftsTicket) {
            return;
        }

        drafts = Array.isArray(answer?.drafts) ? answer.drafts : [];
        notify();
    }).catch((reason: unknown) => {
        // The drafts already shown stay; they are refetched often enough.
        refusal(reason);
    });
}

export function discardDraft(id: string): Promise<void> {
    return deleteDraft(id).then(() => {
        drafts = drafts.filter((draft) => draft.id !== id);
        notify();
        refreshDrafts();
    }).catch((reason: unknown) => {
        const code = refusal(reason);
        if (code === 'draft_gone' || code === 'draft_wiped') {
            refreshDrafts();
        }

        throw reason;
    });
}

// Everything fetched, dropped: the reader is no longer linked, or the plugin
// is going. Requests still on their way are answered to nobody.
export function dropClipCaches(): void {
    generation++;
    listTicket++;
    draftsTicket++;

    // A publish still being looked for stops being looked for when this is
    // the reader unlinking: no armed look outlives it. (Somebody else
    // signing in closes the editor outright, ClipsRoot.)
    if (!linkedNow()) {
        stoppedLooking();
    }
    pendingVotes.clear();
    edits.clear();
    tailReasons.clear();
    const had = list.status !== 'idle' || byId.size > 0 || drafts.length > 0;
    list = {status: 'idle', ids: []};
    byId = new Map();
    drafts = [];
    dropSharedCards();
    if (had) {
        changed();
    }
}

// ---- What the reader has already seen ----

// The newest clip the reader had seen when they last opened the list, kept
// per browser. Only a convenience, so storage that is switched off, full or
// private just means nothing is ever "new".
const SEEN_KEY = 'villanet.clipsSeen';
const SORT_KEY = 'villanet.clipsSort';

// undefined until read; null when storage cannot be used.
let seen: number | null | undefined;

function readSeen(): number | null {
    if (seen === undefined) {
        try {
            const stored = Number(window.localStorage.getItem(SEEN_KEY) || '0');
            seen = Number.isFinite(stored) && stored > 0 ? stored : 0;
        } catch {
            seen = null;
        }
    }

    return seen;
}

// What the reader had seen before now: 0 when they have never looked, null
// when it cannot be known.
export const clipsSeenBefore = (): number | null => readSeen();

export function markClipsSeen(newest: number | undefined): void {
    const before = readSeen();
    if (before === null || !newest || newest <= before) {
        return;
    }

    try {
        window.localStorage.setItem(SEEN_KEY, String(newest));
        seen = newest;
    } catch {
        seen = null;
    }
    notify();
}

// Whether there are clips newer than the newest the reader has seen: the dot
// on the header's clapper and the panel row's NEW.
export function useClipsUnseen(info?: ClipsInfo): boolean {
    const before = useObservable(readSeen);

    return before !== null && typeof info?.newest === 'number' && info.newest > before;
}

// For the lab only: what the reader has seen, set back to a moment so clips
// after it are new again.
export function resetClipsSeen(at: number): void {
    try {
        window.localStorage.setItem(SEEN_KEY, String(Math.max(0, at)));
        seen = Math.max(0, at);
    } catch {
        seen = null;
    }
    notify();
}

export type ClipSort = 'top' | 'new';

// The order the list was last shown in, per browser; New when there is none.
export function rememberedSort(): ClipSort {
    try {
        return window.localStorage.getItem(SORT_KEY) === 'top' ? 'top' : 'new';
    } catch {
        return 'new';
    }
}

export function rememberSort(sort: ClipSort): void {
    try {
        window.localStorage.setItem(SORT_KEY, sort);
    } catch {
        // Not kept; the list opens in the newest-first order next time.
    }
}

// ---- Words every clip surface shares ----

// What a clip with no title is called: its streamer and when it was made,
// "Dave, 3 Oct 21:04", in the reader's own zone.
export function untitledName(streamer: ClipPerson, createdAt: number): string {
    return `${streamer.name}, ${shortStamp(createdAt, viewerZone())}`;
}

export const shownTitleOf = (clip: {title: string; streamer: ClipPerson; createdAt: number}): string =>
    (clip.title !== '' ? clip.title : untitledName(clip.streamer, clip.createdAt));

// ---- Shared clips: the cards in channels (addendum 4.9) ----

// What the cards route said about each shared clip a card in the page has
// asked about, by id, oldest first, so the oldest can go once there are more
// than about 200 (an LRU: a clip read again moves to the end). What was heard
// is asked again on a mount ten minutes on, on Watch, when a clip has gone
// from the server (cardNeedsFetch, watchForGoneClips), and — since a card
// shows its thumbs up (design/3.2.0.md 9.4) — for the cards on screen when
// somebody votes, at most every 30 s (followCardVotes).
//
// One request for every card that mounts within 50 ms of the first — a
// channel opening mounts dozens at once — in batches of 25, the most the
// server takes; and one id is asked about once however many cards show it.
type SharedCard = CardEntry & {card?: ClipCard};

const CARD_LIMIT = 200;
const CARD_BATCH_MS = 50;
const CARDS_PER_REQUEST = 25;

// After a clip goes, every reader's cards would ask at the same moment: each
// waits a moment of its own, up to three seconds, first.
const CARD_SPREAD_MS = 3000;

let sharedCards = new Map<string, SharedCard>();
const cardsQueued = new Set<string>();
let cardsTimer = 0;

// Bumped whenever everything is dropped, so an answer to a request made
// before then is dropped too.
let cardsGeneration = 0;

// Which ids cards in the page are showing, and how many of each: only those
// are asked about again when a clip goes, and none of them is let go to make
// room.
const cardsShown = new Map<string, number>();

// Per id, the cards to tell when what is known about it changes, so a page of
// cards does not re-render them all for one answer.
const cardListeners = new Map<string, Set<Listener>>();

function cardChanged(id: string): void {
    Array.from(cardListeners.get(id) || []).forEach((listener) => listener());
}

function keepCard(id: string, entry: SharedCard): void {
    sharedCards.delete(id);
    sharedCards.set(id, entry);
    cardChanged(id);

    if (sharedCards.size <= CARD_LIMIT) {
        return;
    }

    for (const old of Array.from(sharedCards.keys())) {
        if (sharedCards.size <= CARD_LIMIT) {
            break;
        }
        if (!cardsShown.has(old) && sharedCards.get(old)?.status !== 'loading') {
            sharedCards.delete(old);
        }
    }
}

// A vote's new count and mark on the card a shared post shows, if one is
// held. Kept where it is in the queue for room: a vote is not a read.
function patchSharedCard(id: string, votes: {upvotes: number; voted: boolean}): void {
    const entry = sharedCards.get(id);
    if (!entry?.card || typeof entry.card.upvotes !== 'number') {
        return;
    }

    sharedCards.set(id, {...entry, card: {...entry.card, upvotes: votes.upvotes, voted: votes.voted}});
    cardChanged(id);
}

// The server has said this clip is gone: every card showing it says so.
function sharedCardGone(id: string): void {
    if (sharedCards.has(id)) {
        keepCard(id, {status: 'gone', at: Date.now()});
    }
}

// What a card shows for its clip: the server's card, or where asking for it
// stands. 'idle' before it has been asked for at all.
export function sharedCardOf(id: string): CardFetch {
    const entry = sharedCards.get(id);
    if (!entry) {
        return {status: 'idle'};
    }
    if (entry.card && entry.status !== 'gone') {
        // Asked again with an answer already in hand: the answer stays on
        // show while it is asked, and if asking again fails, rather than
        // flashing back to loading or to "couldn't check".
        return {status: 'ok', card: entry.card};
    }

    return {status: entry.status === 'ok' ? 'error' : entry.status};
}

function sendCards(): void {
    cardsTimer = 0;
    const ids = Array.from(cardsQueued);
    cardsQueued.clear();
    if (ids.length === 0 || !mayWatchClipsNow()) {
        // Unlinked in the 50 ms since they were asked for: nothing is sent,
        // and what was known goes, as everything does when somebody unlinks.
        ids.forEach((id) => {
            sharedCards.delete(id);
            cardChanged(id);
        });

        return;
    }

    const asked = cardsGeneration;
    for (let at = 0; at < ids.length; at += CARDS_PER_REQUEST) {
        const batch = ids.slice(at, at + CARDS_PER_REQUEST);
        fetchClipCards(batch).then((answer) => {
            if (asked !== cardsGeneration) {
                return;
            }

            const now = Date.now();
            const cards = answer?.cards || {};
            batch.forEach((id) => {
                const card = cards[id];
                if (card === null) {
                    keepCard(id, {status: 'gone', at: now});
                } else if (card && typeof card === 'object') {
                    keepCard(id, {status: 'ok', at: now, card});
                } else {
                    keepCard(id, {status: 'error', at: now, card: sharedCards.get(id)?.card});
                }
            });
        }).catch((reason: unknown) => {
            if (asked !== cardsGeneration) {
                return;
            }

            // Not linked: "me" is asked for again (refusal), every card
            // switches to the call to link, and nothing the server said is
            // kept. Anything else is said on the card, which keeps a card it
            // already had.
            const code = refusal(reason);
            const now = Date.now();
            batch.forEach((id) => {
                if (isAccessRefusal(code)) {
                    sharedCards.delete(id);
                    cardChanged(id);
                } else {
                    keepCard(id, {status: 'error', at: now, card: sharedCards.get(id)?.card});
                }
            });
        });
    }
}

// Asks about a clip in the next batch: when what is known is due (on mount),
// or whatever is known (again is true: Watch, and a clip going). Never about
// a clip the server said has gone, and never while the reader is not linked.
function askForCard(id: string, again: boolean): void {
    if (!mayWatchClipsNow() || cardsQueued.has(id)) {
        return;
    }

    const entry = sharedCards.get(id);
    const now = Date.now();
    if (entry?.status === 'gone' || entry?.status === 'loading') {
        return;
    }
    if (!again && !cardNeedsFetch(entry, now)) {
        // Read again: it is not the one to go when room is made.
        if (entry) {
            sharedCards.delete(id);
            sharedCards.set(id, entry);
        }

        return;
    }

    keepCard(id, {status: 'loading', at: now, card: entry?.card});
    cardsQueued.add(id);
    if (cardsTimer === 0) {
        cardsTimer = window.setTimeout(sendCards, CARD_BATCH_MS);
    }
}

// The server's card for a clip shown in a post, while the reader may watch
// clips: asked for when the card mounts (unless what is known is still fresh)
// and whenever the reader becomes able to. id '' asks for nothing: a card that
// is not the reader's to fetch — they are locked, or the clip has gone by the
// post's own word — passes ''.
export function useSharedClip(id: string, linked: boolean | undefined): CardFetch {
    const [, forceRender] = React.useState(0);

    React.useEffect(() => {
        if (id === '') {
            return undefined;
        }

        const listener = () => forceRender((value) => value + 1);
        const set = cardListeners.get(id) || new Set<Listener>();
        set.add(listener);
        cardListeners.set(id, set);
        cardsShown.set(id, (cardsShown.get(id) || 0) + 1);

        return () => {
            set.delete(listener);
            if (set.size === 0) {
                cardListeners.delete(id);
            }
            const shown = (cardsShown.get(id) || 1) - 1;
            if (shown > 0) {
                cardsShown.set(id, shown);
            } else {
                cardsShown.delete(id);
            }
        };
    }, [id]);

    // Asked again, too, after everything was dropped while the card stayed
    // on show (generation): the reader is still linked, and the card would
    // otherwise wait with nothing.
    const generation = cardsGeneration;
    React.useEffect(() => {
        if (id !== '' && linked === true) {
            watchForGoneClips();
            followCardVotes();
            askForCard(id, false);
        }
    }, [id, linked, generation]);

    return id === '' ? {status: 'idle'} : sharedCardOf(id);
}

// Watch on a card: what the server says about the clip now, asked again.
export function refreshSharedClip(id: string): void {
    askForCard(id, true);
}

// A clip went from the server — the snapshot's count went down, or its
// newest clip is older than it was — so any card showing one may now be
// showing a clip that has gone. Every card on show asks again, after a
// moment of its own, and what is known about clips no card shows is marked
// old, so a card that mounts later asks too. Watched from the first linked
// card that asks until somebody signs out or the plugin goes.
let goneWatch: (() => void) | null = null;
let goneTimer = 0;

function watchForGoneClips(): void {
    if (goneWatch) {
        return;
    }

    let last = getSnapshot().clips;
    goneWatch = subscribeToSnapshot(() => {
        const next = getSnapshot().clips;
        const before = last;
        last = next;
        if (!before || !next) {
            return;
        }

        const fewer = next.count < before.count;
        const older = typeof before.newest === 'number' && (typeof next.newest !== 'number' || next.newest < before.newest);
        if (!fewer && !older) {
            return;
        }

        sharedCards.forEach((entry, id) => {
            if (!cardsShown.has(id) && entry.status !== 'gone' && entry.status !== 'loading') {
                sharedCards.set(id, {...entry, at: 0});
            }
        });

        if (goneTimer === 0) {
            goneTimer = window.setTimeout(() => {
                goneTimer = 0;
                Array.from(cardsShown.keys()).forEach((id) => askForCard(id, true));
            }, Math.floor(Math.random() * CARD_SPREAD_MS));
        }
    });
}

// Somebody voted (the clips' version moved): the cards on screen are asked
// about again, by clipLogic's cardRefreshDelay — after a moment of the page's
// own, at most once every 30 s, never for a card off screen, and not while the
// page is hidden, when it waits to be shown. Followed from the first card that
// asks until somebody signs out or the plugin goes.
let votesWatch: (() => void) | null = null;
let votesTimer = 0;
let votesDue = false;
let votesLastAt = 0;
let votesWaitingToBeSeen = false;

function pageVisible(): boolean {
    return typeof document === 'undefined' || document.visibilityState !== 'hidden';
}

function askVotesSoon(): void {
    if (!votesDue || votesTimer !== 0) {
        return;
    }

    const delay = cardRefreshDelay({shown: cardsShown.size, visible: pageVisible(), lastAt: votesLastAt, now: Date.now(), jitter: Math.random()});
    if (delay === null) {
        if (!pageVisible() && !votesWaitingToBeSeen) {
            votesWaitingToBeSeen = true;
            document.addEventListener('visibilitychange', seenAgain);
        }

        return;
    }

    votesTimer = window.setTimeout(() => {
        votesTimer = 0;
        if (!pageVisible() || cardsShown.size === 0) {
            askVotesSoon();

            return;
        }

        votesDue = false;
        votesLastAt = Date.now();
        Array.from(cardsShown.keys()).forEach((id) => askForCard(id, true));
    }, delay);
}

function seenAgain(): void {
    if (!pageVisible()) {
        return;
    }

    votesWaitingToBeSeen = false;
    document.removeEventListener('visibilitychange', seenAgain);
    askVotesSoon();
}

function followCardVotes(): void {
    if (votesWatch) {
        return;
    }

    let last = getSnapshot().clips?.version;
    votesWatch = subscribeToSnapshot(() => {
        const next = getSnapshot().clips?.version;
        const before = last;
        last = next;
        if (!before || !next || before === next) {
            return;
        }

        votesDue = true;
        askVotesSoon();
    });
}

function stopFollowingCardVotes(): void {
    votesWatch?.();
    votesWatch = null;
    window.clearTimeout(votesTimer);
    votesTimer = 0;
    votesDue = false;
    votesLastAt = 0;
    if (votesWaitingToBeSeen) {
        votesWaitingToBeSeen = false;
        document.removeEventListener('visibilitychange', seenAgain);
    }
}

// Everything the cards route said, dropped: the reader is no longer linked,
// somebody else signed in, or the plugin is going. A request on its way is
// answered to nobody.
function dropSharedCards(): void {
    cardsGeneration++;
    window.clearTimeout(cardsTimer);
    cardsTimer = 0;
    cardsQueued.clear();
    const ids = new Set([...Array.from(sharedCards.keys()), ...Array.from(cardListeners.keys())]);
    sharedCards = new Map();
    ids.forEach(cardChanged);
}

// "me" asked for once by the cards, when Mattermost says somebody is signed
// in and the plugin has no "me" for them: somebody who signed in after the
// page loaded (addendum 2.2), whose cards would otherwise wait for the panel
// to be opened. index.tsx asks too, as the store says who signed in; the
// requests share one answer (refreshIdentity).
let meAsked = false;

export function askForMeOnce(): void {
    if (meAsked || getIdentity() !== null || currentUserId() === '') {
        return;
    }

    meAsked = true;
    refreshIdentity();
}

// ---- Shared clips: Watch, a clip link, and Share ----

// Watch on a shared clip's card: the Clips dialog on that clip in the window
// it was pressed in, and in the main window the panel opened behind it,
// as the profile popover's Watch opens it (addendum 4.8). A popped-out window
// shows the dialog only. For somebody who may not watch, the clip waits and
// the lock opens instead (lockPending.ts openLock); unlocking opens it.
export function watchSharedClip(id: string): void {
    if (mayWatchClipsNow()) {
        setDialog({view: 'clip', id, direct: true});
        if (windowKind() === 'main') {
            openPanel();
        }

        return;
    }

    setDialog({view: 'locked', clipId: id});
    openLock({kind: 'clip', clipId: id});
}

// A clip link's page (ClipLinkRoute) hands over here before it sends
// Mattermost on to the team: the dialog is set on the clip — or on the call
// to link, for somebody known not to be linked — to show as soon as the app
// is drawn, and the panel is to open once there is a channel to open it in
// (installClipArrival).
let panelOnArrival = false;

export function openClipFromLink(id: string): void {
    if (mayWatchClipsNow()) {
        setDialog({view: 'clip', id, direct: true});
    } else {
        setDialog({view: 'locked', clipId: id});
        openLockOnArrival(id);
    }
    panelOnArrival = true;
}

// A clip link for somebody who may not watch it: the clip waits for the lock
// to open, as a card's Watch makes it wait, and the panel opened on arrival
// is the lock, opened by the person's own press of the link.
function openLockOnArrival(id: string): void {
    lockOnArrival = id;
}

let lockOnArrival = '';

// How long a clip link waits for Mattermost to have a channel open before it
// stops waiting to open the panel. Somebody in no team gets Mattermost's team
// picker, and the dialog without the panel.
const ARRIVAL_MS = 15000;

let arrival: (() => void) | null = null;

type StoreLike = {subscribe: (listener: () => void) => () => void};

// Opens the panel once Mattermost, sent on from a clip link, has a
// channel open: the side panel lives in the channel view, so asked any
// earlier it would have nowhere to be. Once, and not at all after 15 s.
export function installClipArrival(store: StoreLike): void {
    arrival?.();
    if (!panelOnArrival) {
        return;
    }

    let unsubscribe: (() => void) | null = null;
    let timer = 0;
    const stop = () => {
        window.clearTimeout(timer);
        unsubscribe?.();
        unsubscribe = null;
        arrival = null;
        panelOnArrival = false;
        lockOnArrival = '';
    };
    const check = () => {
        if (panelOnArrival && currentChannelId() !== '') {
            const waiting = lockOnArrival;
            stop();
            if (waiting !== '') {
                openLock({kind: 'clip', clipId: waiting});
            } else {
                markOpenedByHand();
                openPanel();
            }
        }
    };

    arrival = stop;
    timer = window.setTimeout(stop, ARRIVAL_MS);
    unsubscribe = store.subscribe(check);
    check();
}

// Share (addendum 4.10): the clip posted into a channel as the reader. A
// refusal saying the reader is not linked has "me" asked for again, as every
// clip request's does; a clip that has gone is forgotten.
export function shareOn(id: string, channelId: string): Promise<{postId: string; channelId: string}> {
    return shareClip(id, channelId).catch((reason: unknown) => {
        if (refusal(reason) === 'clip_gone') {
            forget(id);
        }

        throw reason;
    });
}

// ---- The ends of streams, still held ----

// The snapshot's clipTails that are still held by this browser's clock, less
// those of streams that are live again; and the surface asks again the moment
// the first of them goes, so "just ended" and Clip the end go with it rather
// than at the next snapshot, which can be half a minute away.
export function useHeldTails(tails: ClipTail[] | undefined, live?: string[]): ClipTail[] {
    const [, tick] = React.useReducer((count: number) => count + 1, 0);
    const held = heldTails(tails, Date.now(), live);
    const next = nextTailEnd(held);

    React.useEffect(() => {
        if (next === 0) {
            return undefined;
        }

        // A little past the end, so the look cannot land a millisecond short.
        const timer = window.setTimeout(tick, Math.max(0, next - Date.now()) + 20);

        return () => window.clearTimeout(timer);
    }, [next]);

    return held;
}

// ---- The streamer's own controls ----

// "Allow clips of my stream" (spec 2.10.6, addendum 3.5): the stream the
// reader has linked, kept for clipping or not. Switching it off wipes what
// is held of it and every draft cut from it, the reader's own included, so
// their drafts are asked for again. Resolves to what the server says it is
// now.
export function allowClipsOfMine(allow: boolean): Promise<boolean> {
    return setClipsAllowed(allow).then((answer) => {
        if (!allow) {
            refreshDrafts();
        }

        return Boolean(answer?.allowed);
    }).catch((reason: unknown) => {
        refusal(reason);

        throw reason;
    });
}

// "Let people download clips of my stream" (design/3.2.1.md 1.3): every clip
// of the stream the reader has linked, past and future. Turning it off
// deletes nothing. Resolves to what the server says it is now.
export function allowDownloadsOfMine(downloads: boolean): Promise<boolean> {
    return setClipDownloads(downloads).then((answer) => Boolean(answer?.downloads)).catch((reason: unknown) => {
        refusal(reason);

        throw reason;
    });
}

// Wiping what is held of a stream of the reader's: the recording so far, or
// the end of it after it went off, and every draft cut from it, theirs
// included. Published clips stay. Resolves to how many drafts went.
export function wipeMine(handle: string): Promise<number> {
    return wipeRecording(handle).then((answer) => {
        refreshDrafts();

        return Math.max(0, Number(answer?.draftsRemoved) || 0);
    }).catch((reason: unknown) => {
        refusal(reason);

        throw reason;
    });
}

// How many clips of the reader's own stream the last list fetched had, for
// the footer's "3 clips of your stream". The list, not the snapshot: which
// clips are of whose stream is clip content, which only the list carries. 0
// until the list has been fetched — by opening Clips.
export function useClipsOfMine(): number {
    const shown = useClipList();
    if (shown.status !== 'ready') {
        return 0;
    }

    return shown.ids.filter((id) => byId.get(id)?.ofMyStream === true).length;
}

// ---- Going away ----

// Everything back to how it was before the plugin loaded: the dialog and the
// editor closed and everything fetched dropped. For uninitialize, and for
// somebody signing out.
export function resetClips(): void {
    dialog = null;
    editor = null;
    editorTicket++;
    stopChecking();
    dropClipCaches();
    arrival?.();
    panelOnArrival = false;
    lockOnArrival = '';
    meAsked = false;
    goneWatch?.();
    goneWatch = null;
    window.clearTimeout(goneTimer);
    goneTimer = 0;
    stopFollowingCardVotes();
    notify();
}
