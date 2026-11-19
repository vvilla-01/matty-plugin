import React from 'react';
import {render} from 'react-dom';
import {createRoot} from 'react-dom/client';

import {PanelGate} from '../src/components/PanelGate';
import {installAccess, messageWillBePosted, signedOut, socketReconnected} from '../src/access';
import {ME_EVENT, meHeard} from '../src/me';
import {advanceClock} from '../src/clock';
import {latestAccess, onIdentityChange} from '../src/identity';
import {openerPress} from '../src/lockPending';
import {slowLockOpening} from '../src/motion';
import type {WindowKind} from '../src/away';
import {PopoverLive} from '../src/components/PopoverLive';
import {PopoverTakebacks} from '../src/components/PopoverTakebacks';
import {PostAwardBadges} from '../src/components/AwardBadges';
import {PLAN_POST_TYPE} from '../src/planLogic';
// Not used, imported for its type check: see mocks/surface.ts.
import './mocks/surface';

import {SidebarTitle} from '../src/components/SidebarTitle';
import {failOpenStreams, forgetViewers, labCounters, resetLabCounters, setTuneDelay} from './mocks/whep';
import {labRefusal, voteClip} from './mocks/client';
import {ConsolePage} from './consolePage';
import {followSignIn, getIdentity, refreshIdentity, setIdentity, useIdentity} from '../src/identity';
import {takeBrand} from '../src/name';
import {ClipsRoot} from '../src/components/ClipsRoot';
import {openClips, refreshDrafts, resetClips, resetClipsSeen, startClip} from '../src/clips';
import {setSnapshot} from '../src/store';
import {forgetTagSearch} from '../src/tagSearch';
import {injectStyles} from '../src/styles';
import {addWatching, getWatching, installReconciler, setSidebarOpener} from '../src/watching';
import {installTwitch, watchOrOpen} from '../src/twitch';
import {installSessions, lastSessionStop, sessionCount} from '../src/sessions';
import {getAway, installAway, pretendWindowKind, useAway} from '../src/away';
import {MiniPlayerHost} from '../src/components/MiniPlayer';
import {CLIP_POST_TYPE} from '../src/clipLogic';

import {
    LAB_OTHER_PLUGIN,
    LAB_OTHER_TEAM,
    LAB_PANEL,
    connectLabMattermost,
    getReplyFails,
    labCloseSidePanel,
    labDispatch,
    labReplyFromElsewhere,
    labRetryReply,
    labSay,
    labSendReply,
    labSetPreference,
    labShowPanel,
    labStore,
    labTogglePanel,
    rootsIn,
    setReplyFails,
    useLabSaid,
    useLabState,
} from './mattermost';
import type {LabPost} from './mattermost';
import {PLUGIN_ID} from './mocks/manifest';
import type {LetInPerson} from '../src/types';
import type {LabLinkAnswer, LabStreamer} from './world';
import {LabCentre, LabClipLink, LabPostBody, LabSharedClips, postTypeOf, useClipLink} from './sharedClips';
import {LabGallery320, Rows320, labHooks320, useGallery} from './rows320';
import {Rows340, labHooks340, playingStaysAtStart} from './rows340';
import {clearAccounts, followAccountsAccess} from '../src/accounts';
import {installBoards, loadBoards} from '../src/boards';
import {labClipPlayer} from './clipPlayer';
import {installLabPostUtils} from './atMention';
import {installLabEmoticon} from './mocks/emojiImages';

import {
    HOUR_MS,
    LAB_CHANNELS,
    LAB_TEAM,
    LAB_USERS,
    MINUTE_MS,
    ME_ID,
    change,
    clearAllNowPlaying,
    clearClips,
    dropFor,
    emptyWatchBoards,
    ensureDaveLive,
    everybodyLive,
    everybodyLiveWithTails,
    getWorld,
    goLive,
    goOffline,
    isTwitchLive,
    labTwitchLedgerOf,
    labVictorTwitchOf,
    pauseErinOnePoll,
    setTwitch,
    setTwitchLedger,
    setVictorTwitch,
    toggleErinBroadcastBox,
    toggleTwitchLive,
    victorSimulcast,
    hasLongClipTitle,
    hasLongName,
    hytaleNowPlaying,
    labAccessOf,
    labStreamerOf,
    labTagOf,
    memoryOnlyMediaCache,
    meOf,
    erinFilmOn,
    pastMonthsOnly,
    plan,
    rememberedMediaCache,
    reset,
    restoreBoards,
    restoreClips,
    restoreNowPlaying,
    restoreWatchBoards,
    sampleLetIn,
    setLabBestClipper,
    setLabDrafts,
    setLabRecording,
    setLabStreamer,
    setLabTail,
    setDaveTag,
    snapshotOf,
    subscribe,
    toggleLongClipTitle,
    toggleLongName,
    toggleVictorSnoops,
    toggleRadio,
    topSnooperIn,
    unplan,
    unusedMediaCache,
    RADIO_HANDLE,
    labAdminNews,
    setLabAdminNews,
} from './world';

// The lab.
//
// The right-hand column is the REAL panel: the same PanelGate component (the
// lock, or the Sidebar behind it, as Mattermost's side panel shows), the same
// stylesheet, the same date handling. Only the edges are swapped out by
// webpack — the HTTP client, the WebRTC player and Twitch's player and
// windows (see webpack.config.js) — so what is on screen here is what will be
// on screen in Mattermost.
//
// The left-hand column is scaffolding: it pokes the fake server so situations
// that are a nuisance to arrange on a live server (four people live, a stream
// starting in ninety seconds, a save that fails) can be looked at in a second.

// How the page is mounted, which decides how React batches updates. Mattermost
// 11.11 runs React 18.2 but mounts its page with ReactDOM.render, a legacy root
// that batches as React 17 did; only a server with the EnableConcurrentReact
// feature flag on (off by default) uses createRoot and React 18's automatic
// batching (webapp/channels/src/entry.tsx, renderReactRootComponent). The lab
// mounts the default way, and the other way when opened with ?root=concurrent,
// so the panel can be looked at under both.
const CONCURRENT_ROOT = new URLSearchParams(window.location.search).get('root') === 'concurrent';

function remountAs(concurrent: boolean): void {
    if (concurrent === CONCURRENT_ROOT) {
        return;
    }

    // A root cannot change kind, so the page loads again with the lab's own
    // state left behind, as a reload of Mattermost would leave it.
    const url = new URL(window.location.href);
    if (concurrent) {
        url.searchParams.set('root', 'concurrent');
    } else {
        url.searchParams.delete('root');
    }
    window.location.assign(url.toString());
}

const PEOPLE: Array<{name: string; userId: string}> = [
    {name: 'Victor', userId: ME_ID},
    {name: 'Dave', userId: 'u-dave'},
    {name: 'Bob', userId: 'u-bob'},
    {name: 'Erin', userId: 'u-erin'},
];

const WIDTHS: Array<{label: string; px: number; note: string}> = [
    {label: '304', px: 304, note: 'narrower still, for the Twitch states (design/twitch-live.md 11.3)'},
    {label: 'Narrow', px: 320, note: 'the smallest the sidebar goes'},
    {label: '360', px: 360, note: 'below it the footer bar’s title keeps only its pencil, with the soundboard there (design/3.2.0.md 2.7)'},
    {label: 'Default', px: 400, note: 'what almost everybody sees, and the widest below a 1201 px window'},
    {label: '464', px: 464, note: 'Mattermost’s widest in a 1201–1680 px window: the narrowest where Twitch’s player plays in the panel'},
    {label: 'Wide', px: 560, note: 'dragged out'},
    {label: '776', px: 776, note: 'Mattermost’s widest from a 1681 px window: two tiles a row'},
    {label: 'Popped out', px: 900, note: 'its own window'},
];

function useWorld() {
    const [, bump] = React.useState(0);
    React.useEffect(() => subscribe(() => bump((value) => value + 1)), []);

    return getWorld();
}

// How Mattermost answers a hashtag search; see world.ts.
const TAG_SEARCH: Array<{mode: 'messages' | 'none' | 'fails' | 'off'; label: string; note: string}> = [
    {mode: 'messages', label: 'messages', note: 'The sample messages: #milsim has three pages of them, the first one short, #arma3 every kind of channel, #soulslike a few'},
    {mode: 'none', label: 'none', note: 'Nothing tagged with anything'},
    {mode: 'fails', label: 'fails', note: 'Mattermost too busy to answer, as a 503'},
    {mode: 'off', label: 'search off', note: 'A server with message search switched off, as a 501'},
];

// What the lock's Unlock is answered with next (mocks/client.ts linkStream).
const LINK_ANSWERS: Array<[LabLinkAnswer, string, string]> = [
    ['success', 'success', 'linked, once a token of 6 or more characters is entered (the lab never holds a real one); “Bearer ” on its own, or nothing, is not sent at all'],
    ['not_accepted', '403 not accepted', '“That token was not accepted. Check it and try again.”'],
    ['rate_limited', '429', '“Too many tries. Wait a few minutes, then try again.”'],
    ['unreachable', '502 unreachable', '“Broadcast Box is not answering, so your token can’t be checked. Try again later.”'],
    ['bad_name', '502 bad name', '“Your token was accepted, but its stream name can’t be used here. Ask an admin to look at it.”'],
    ['not_configured', '503 not set up', '“Matty is not set up yet, so it can’t be unlocked.”'],
    ['too_long', '400 too long', '“That is too long to be a bearer token.”'],
    ['store', '500 store', '“Your token was accepted but could not be saved. Try again in a moment.”'],
    ['bad_request', '400 bad request', '“That did not get through. Try again.”'],
    ['empty_key', '200, no key', 'a 200 whose streamKey is empty, worded as not accepted'],
    ['no_answer', 'no answer', 'the plugin restarting (404): “Matty is not answering. Try again in a moment.”'],
];

// Mattermost's search bar as 11.11 draws it (components/new_search/
// new_search.tsx and search_box.tsx): a button, #searchFormContainer, showing
// the store's words, that opens a box, #searchBox, outside it (Mattermost's is
// in a portal). The box's search input copies the store's words once, as it
// opens, searches on Enter and closes on Escape or a click outside it.
// "Open in Mattermost search" in the plugin's results puts its words in the
// store, presses the button and presses Enter in the input, as it would on
// the real one.
const SearchBar: React.FC = () => {
    const terms = useLabState().views.rhs.searchTerms;
    const said = useLabSaid();
    const [open, setOpen] = React.useState(false);
    const [typed, setTyped] = React.useState('');
    const box = React.useRef<HTMLDivElement | null>(null);

    // Read from the store at the press, not from this render: the plugin
    // presses the button straight after changing the words.
    const openBox = () => {
        setTyped(String(labStore.getState()?.views?.rhs?.searchTerms || ''));
        setOpen(true);
    };

    React.useEffect(() => {
        if (!open) {
            return undefined;
        }

        const outside = (event: MouseEvent) => {
            if (box.current && !box.current.contains(event.target as Node) && !(event.target as Element).closest?.('#searchFormContainer')) {
                setOpen(false);
            }
        };
        document.addEventListener('click', outside, {capture: true});

        return () => document.removeEventListener('click', outside, {capture: true});
    }, [open]);

    const search = () => {
        const words = typed.trim();
        setOpen(false);
        if (words === '') {
            return;
        }

        labDispatch({type: 'UPDATE_RHS_SEARCH_TERMS', terms: typed});
        labDispatch({type: 'UPDATE_RHS_STATE', state: 'search'});
        labSay(`Mattermost would search for ${words}, with the results in place of the panel.`);
    };

    return (
        <div className='lab-search-wrap'>
            <div
                id='searchFormContainer'
                className='lab-search'
                role='button'
                tabIndex={0}
                onClick={openBox}
                onKeyDown={(event) => {
                    if (event.key === 'Enter' || event.key === ' ') {
                        event.preventDefault();
                        openBox();
                    }
                }}
            >{terms || 'Search'}</div>
            {open ? (
                <div
                    id='searchBox'
                    ref={box}
                    className='lab-search-box'
                >
                    <input
                        className='lab-search-input'
                        type='search'
                        aria-label='Search messages'
                        autoFocus={true}
                        value={typed}
                        onChange={(event) => setTyped(event.target.value)}
                        onKeyDown={(event) => {
                            if (event.key === 'Enter') {
                                event.preventDefault();
                                search();
                            } else if (event.key === 'Escape') {
                                event.preventDefault();
                                setOpen(false);
                            }
                        }}
                    />
                </div>
            ) : null}
            <span className='lab-note'>{said || 'Nothing asked of Mattermost yet.'}</span>
        </div>
    );
};

const Row: React.FC<{label: string; children: React.ReactNode}> = ({label, children}) => (
    <div className='lab-row'>
        <span className='lab-label'>{label}</span>
        <div className='lab-controls'>{children}</div>
    </div>
);

type ControlProps = {
    width: number;
    onWidth: (px: number) => void;
    showingConsole: boolean;
    onConsole: (next: boolean) => void;

    // The panel in a window of its own, laid out the way Mattermost lays
    // that window out — which is where its footer used to fall off the bottom.
    popout: boolean;
    onPopout: (next: boolean) => void;

    // The channel-change effect only exists WHILE a stream is being found, so
    // the lab needs a way to make that take long enough to look at.
    tuning: boolean;
    onTuning: (next: boolean) => void;

    // Takes the panel out of the page and puts it back, after gapMs: zero is
    // a remount in one render, anything more lets the browser pause what was
    // taken out before it comes back.
    onRemount: (gapMs: number) => void;

    // A channel switch: Mattermost renders a new message list.
    onChannelSwitch: () => void;
};

// The lab's own switches for the side panel, outside React so the chips and
// the parts of the page they change agree without being passed around.
// signingIn is the Access row's "signed in after load" or "signed out, back
// unlinked", while the page sits on Mattermost's sign-in page waiting for
// somebody to sign in; replaced is its "replaced elsewhere", while "me" still
// says linked and the server no longer does.
const labSwitches = {
    delayMini: false,
    freezePushes: false,
    signingIn: false,
    stealKeys: true,

    // The lock: the WebSocket down ("me" events dropped), Mattermost's own
    // move of focus into the side panel, the unlock slowed tenfold, and "me"
    // held as the page knew it while the lab's server changes underneath.
    socketDown: false,
    rhsFocus: false,
    slowUnlock: false,
    held: false,
};
const switchListeners = new Set<() => void>();

function flipSwitch(name: keyof typeof labSwitches): void {
    setSwitch(name, !labSwitches[name]);
}

function setSwitch(name: keyof typeof labSwitches, value: boolean): void {
    labSwitches[name] = value;
    switchListeners.forEach((listener) => listener());
}

function useSwitches(): typeof labSwitches {
    const [, bump] = React.useState(0);
    React.useEffect(() => {
        const listener = () => bump((value) => value + 1);
        switchListeners.add(listener);

        return () => {
            switchListeners.delete(listener);
        };
    }, []);

    return labSwitches;
}

// Dave live and in the panel, for the rows that show his scissors.
function watchDave(): string {
    const handle = ensureDaveLive();
    if (handle) {
        addWatching(handle);
    }

    return handle;
}

// Mattermost's message box takes any printable key pressed outside a text
// field, and a paste (Ctrl/Cmd+V) from anywhere that is not an INPUT or a
// TEXTAREA, a button included (use_textbox_focus.tsx, shouldFocusMainTextbox
// in post_utils.ts): focus moves to it during the keydown, and the letter or
// the paste lands there. A listener on the document in the bubble phase, as
// Mattermost's is, so a key the panel stops in its own handlers never reaches
// it — which is the point of looking. Space is spared on a button or a link,
// as there.
//
// A paste by Mattermost's own test, copied here rather than taken from the
// plugin, so the lab can catch the plugin disagreeing with it
// (utils/keyboard.ts cmdOrCtrlPressed and isKeyPressed): Cmd on a Mac, Ctrl
// without Alt elsewhere; V by the letter, or else by keyCode 86 — which is
// what a Russian or Greek layout's Ctrl+V gives, its letter being м or ω.
const labOnMac = /Mac|iPhone|iPad/.test(navigator.platform);

function mattermostSeesPaste(event: KeyboardEvent): boolean {
    const cmdOrCtrl = labOnMac ? event.metaKey : (event.ctrlKey && !event.altKey);
    if (!cmdOrCtrl || event.keyCode === 229) {
        return false;
    }
    if (typeof event.key !== 'undefined' && event.key !== 'Unidentified' && event.key !== 'Dead' && (event.key === 'v' || event.key === 'V')) {
        return true;
    }

    return event.keyCode === 86;
}

document.addEventListener('keydown', (event) => {
    const command = event.ctrlKey || event.metaKey;
    const paste = mattermostSeesPaste(event);
    if (!labSwitches.stealKeys || (!paste && (event.key.length !== 1 || command || event.altKey))) {
        return;
    }

    const active = document.activeElement as HTMLElement | null;
    if (active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA' || active.closest('[contenteditable="true"]'))) {
        return;
    }
    if (!paste && event.key === ' ' && active && (active.tagName === 'BUTTON' || active.tagName === 'A')) {
        return;
    }

    const box = document.getElementById('post_textbox');
    if (box && box !== active) {
        box.focus();
        labSay(`Mattermost took ${paste ? 'a paste' : `“${event.key}”`} to the message box, from ${labDescribe(active)}.`);
    }
});

// An element as the lab's notes name it: its tag and first class, its id
// when it has no class.
function labDescribe(element: HTMLElement | null): string {
    if (!element) {
        return 'nothing';
    }

    const firstClass = typeof element.className === 'string' ? element.className.split(' ')[0] : '';
    if (firstClass) {
        return `${element.tagName.toLowerCase()}.${firstClass}`;
    }

    return element.id ? `${element.tagName.toLowerCase()}#${element.id}` : element.tagName.toLowerCase();
}

// Safari, for the Sound row: it plays a clip's pictures but cannot play its
// Opus sound, which is what canPlayType says.
const realCanPlayType = HTMLMediaElement.prototype.canPlayType;
HTMLMediaElement.prototype.canPlayType = function canPlayType(type: string): CanPlayTypeResult {
    if (getWorld().noSound && /^audio\/mp4/i.test(type) && /opus/i.test(type)) {
        return '';
    }

    return realCanPlayType.call(this, type);
};

// The newest message in the channel, which is the one the side-panel chips
// reply to.
function newestRoot(channelId: string): string {
    const roots = rootsIn(channelId);

    return roots[roots.length - 1]?.id || '';
}

// Typing in the reply box, as a person does it: the words go in and an input
// event says so, which is what the panel listens for.
function typeInReply(words: string): void {
    const box = document.querySelector('#sidebar-right #reply_textbox');
    if (!(box instanceof HTMLTextAreaElement)) {
        labSay('There is no reply box: open a thread first.');

        return;
    }

    box.value = words;
    box.dispatchEvent(new Event('input', {bubbles: true}));
}

// Enter in the reply box: whatever is in it is sent, and the box is emptied,
// as Mattermost empties the draft.
function sendReply(root: string): void {
    const box = document.querySelector('#sidebar-right #reply_textbox');
    const typed = box instanceof HTMLTextAreaElement ? box.value.trim() : '';
    if (box instanceof HTMLTextAreaElement) {
        box.value = '';
    }

    labSendReply(root, typed || 'Agreed, see you there.');
}

const Controls: React.FC<ControlProps> = ({width, onWidth, showingConsole, onConsole, tuning, onTuning, popout, onPopout, onRemount, onChannelSwitch}) => {
    const world = useWorld();
    const known = useIdentity();
    let adminMode: 'no' | 'linked' | 'unlinked' = 'no';
    if (world.me.isAdmin) {
        adminMode = world.me.streamKey ? 'linked' : 'unlinked';
    }
    const labState = useLabState();
    const phoneSized = labState.views.browser.windowSize === 'mobileView';
    const rhs = labState.views.rhs;
    const away = useAway();
    const switches = useSwitches();
    let accessMode: string = known ? latestAccess() : 'unknown';
    if (switches.signingIn) {
        accessMode = signingInAs;
    }
    const [kind, setKind] = React.useState<WindowKind>('main');
    const [failing, setFailing] = React.useState(getReplyFails());
    const channelId = labState.entities.channels.currentChannelId;
    const threadOpen = rhs.isSidebarOpen && rhs.selectedPostId !== '' && rhs.rhsState !== 'edit-history';
    const preference = (name: string, fallback: string) =>
        labState.entities.preferences.myPreferences[`${`pp_${PLUGIN_ID}`.slice(0, 32)}--${name}`]?.value || fallback;
    const returnAfter = preference('return_after_reply', 'reply');
    const mini = preference('mini_player', 'on');

    // Whose profile the popover below is, and whether it sits on one of
    // Mattermost's light themes. Everybody's is different — Dave is twice a
    // monthly champion, Erin once, Bob never first — and the rows set no text
    // colour of their own, so white is the only way to see them as most
    // people will.
    const [profileFor, setProfileFor] = React.useState('u-dave');
    const [lightPopover, setLightPopover] = React.useState(false);

    // Whose post the header below is, and on which theme, for the same
    // reason: the badges are on a different person each, and a post sits on
    // Mattermost's centre channel, which is white for most people. Erin by
    // default, as the Top Snooper; Victor has the crown.
    const [postBy, setPostBy] = React.useState('u-erin');
    const [lightPost, setLightPost] = React.useState(false);

    const liveHandles = new Map(world.streams.map((item) => [item.userId || '', item.handle]));

    const toggleLive = (userId: string, name: string) => {
        const handle = liveHandles.get(userId);
        if (handle) {
            goOffline(handle);
        } else {
            goLive(name, userId);
        }
    };

    const tomorrowEvening = () => {
        const at = new Date(Date.now() + (24 * HOUR_MS));
        at.setHours(20, 0, 0, 0);

        return at.getTime() - Date.now();
    };

    return (
        <div className='lab-panel'>
            <h1 className='lab-title'>{'Matty Plugin lab'}</h1>
            <p className='lab-blurb'>
                {'The panel on the right is the real one. Nothing here reaches a network — the fake server lives in this page.'}
            </p>

            <Row label='Panel width'>
                {WIDTHS.map((option) => (
                    <button
                        key={option.px}
                        type='button'
                        className='lab-chip'
                        aria-pressed={width === option.px}
                        title={option.note}
                        onClick={() => onWidth(option.px)}
                    >{option.label}</button>
                ))}
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={popout}
                    title='Mattermost’s “open in a new window”: the whole window, with classification banners'
                    onClick={() => onPopout(!popout)}
                >{'Popout window'}</button>
            </Row>

            <Rows340
                Row={Row}
                onWidth={onWidth}
            />

            <Rows320 Row={Row}/>

            <Row label='Side panel'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={rhs.isSidebarOpen && rhs.rhsState === 'plugin' && rhs.pluggableId === LAB_PANEL}
                    title='The plugin’s App Bar icon: closes the panel when it is showing, which hangs up every stream at once; opens it otherwise, picking up the streams without reconnecting'
                    onClick={toggleByButton}
                >{'App Bar icon'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Reply on the newest message from the keyboard (R in its menu, or Shift+Up): the thread takes the panel’s place, remembering what was there, and an expanded panel stays expanded. The arrows on the messages in the middle are the mouse, which collapses it first.'
                    onClick={() => labDispatch({type: 'SELECT_POST', postId: newestRoot(channelId), channelId, timestamp: Date.now(), previousRhsState: rhs.rhsState})}
                >{'Reply'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={threadOpen}
                    title='A click on the newest message (Click to open threads): the thread takes the panel’s place'
                    onClick={() => labDispatch({type: 'SELECT_POST', postId: newestRoot(channelId), channelId, timestamp: Date.now()})}
                >{'Open thread'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    disabled={!threadOpen}
                    title='Words in the reply box, as if typed: during the wait after a reply this cancels the panel coming back, and left there it keeps the next reply from bringing it back'
                    onClick={() => typeInReply('Still typing…')}
                >{'Type in reply'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    disabled={!threadOpen}
                    title='Enter in the reply box: a placeholder at once, the real post 400 ms later, and the panel back 1.5 s after that'
                    onClick={() => sendReply(rhs.selectedPostId)}
                >{'Send reply'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={failing}
                    title='Replies fail: the placeholder comes back marked failed, with a Retry in the thread'
                    onClick={() => {
                        setReplyFails(!failing);
                        setFailing(!failing);
                    }}
                >{'Reply fails'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    disabled={!threadOpen}
                    title='The same person replies in this thread from their phone: the post arrives with no placeholder in this tab, so the panel stays where it is'
                    onClick={() => labReplyFromElsewhere(rhs.selectedPostId)}
                >{'Reply from another device'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    disabled={!rhs.isSidebarOpen}
                    title='The X, or Ctrl+., on whatever is in the side panel: closeRightHandSide, one batch'
                    onClick={labCloseSidePanel}
                >{'Close side panel'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={rhs.rhsState === 'search'}
                    onClick={() => labDispatch({type: 'UPDATE_RHS_STATE', state: 'search'})}
                >{'Search'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    disabled={!threadOpen || !rhs.previousRhsState}
                    title='The Back arrow on a thread opened from search results'
                    onClick={() => labDispatch({type: 'RHS_GO_BACK', state: rhs.previousRhsState})}
                >{'Back to results'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={rhs.rhsState === 'mention'}
                    onClick={() => labDispatch({type: 'UPDATE_RHS_STATE', state: 'mention'})}
                >{'Mentions'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={rhs.rhsState === 'plugin' && rhs.pluggableId === LAB_OTHER_PLUGIN}
                    onClick={() => labDispatch({type: 'UPDATE_RHS_STATE', state: 'plugin', pluggableId: LAB_OTHER_PLUGIN})}
                >{'Another plugin'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={labState.views.rhsSuppressed}
                    title='Mattermost’s Threads page, which hides the side panel without changing what is in it'
                    onClick={() => labDispatch({type: labState.views.rhsSuppressed ? 'UNSUPPRESS_RHS' : 'SUPPRESS_RHS'})}
                >{'Threads page'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={labState.entities.teams.currentTeamId !== LAB_TEAM.id}
                    title='Another team: Mattermost changes the team, then closes the side panel, which switches the television off'
                    onClick={() => {
                        const elsewhere = labState.entities.teams.currentTeamId === LAB_TEAM.id;
                        labDispatch({type: 'SELECT_TEAM', data: elsewhere ? LAB_OTHER_TEAM.id : LAB_TEAM.id});
                        labCloseSidePanel();
                    }}
                >{'Switch team'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Town Square and Arma night in turn: a new message list, and Arma night has a bookmarks bar, so the list starts lower (in a window under 595px tall, short enough for the small player’s bar)'
                    onClick={onChannelSwitch}
                >{'Channel switch'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={rhs.isSidebarExpanded}
                    disabled={!rhs.isSidebarOpen}
                    title='The side panel expanded over the channel'
                    onClick={() => labDispatch({type: 'SET_RHS_EXPANDED', expanded: !rhs.isSidebarExpanded})}
                >{'Expanded'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Unmount the panel and mount it again in the same render: the pictures carry on, with no new connection'
                    onClick={() => onRemount(0)}
                >{'Remount panel'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Unmount the panel and mount it again 400 ms later, long enough for the browser to pause the pictures it took out: they resume, with no new connection and no “Playback keeps stopping”'
                    onClick={() => onRemount(400)}
                >{'Remount after a gap'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Every open stream’s connection fails, as when ICE gives up: the picture freezes and Retry reconnects it'
                    onClick={failOpenStreams}
                >{'Connections fail'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={switches.delayMini}
                    title='The small player mounts a frame after the panel goes, so the picture spends that frame out of the page and the browser pauses it: it must carry on with no “Playback keeps stopping”'
                    onClick={() => flipSwitch('delayMini')}
                >{'Delay mini mount by one frame'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={switches.freezePushes}
                    title='The server stops pushing: only the 30-second poll of whatever is showing streams keeps the list, and the preview stills, current'
                    onClick={() => flipSwitch('freezePushes')}
                >{'Freeze pushes'}</button>
                <span className='lab-note'>
                    {`Panel: ${away.phase}${away.cause ? ` (${away.cause})` : ''}, side panel: ${away.panel}`}
                    {away.phase === 'away' ? `, small player: ${away.latched || 'nothing'}${away.hidden ? ', hidden' : ''}` : ''}
                    {away.returningUntil > 0 ? ', coming back…' : ''}
                    {away.undo ? `, Back to thread for ${away.undo.root}` : ''}
                </span>
            </Row>

            <Row label='Settings'>
                {(['reply', 'close', 'never'] as const).map((value) => (
                    <button
                        key={value}
                        type='button'
                        className='lab-chip'
                        aria-pressed={returnAfter === value}
                        title='Settings › Plugin preferences › Matty › After replying to a message (the tab takes the server’s name)'
                        onClick={() => labSetPreference('return_after_reply', value)}
                    >{`back: ${value}`}</button>
                ))}
                {(['on', 'off'] as const).map((value) => (
                    <button
                        key={value}
                        type='button'
                        className='lab-chip'
                        aria-pressed={mini === value}
                        title='Settings › Plugin preferences › Matty › While something else is in the side panel'
                        onClick={() => labSetPreference('mini_player', value)}
                    >{`small player: ${value}`}</button>
                ))}
            </Row>

            <Row label='Media'>
                <MediaCounters/>
            </Row>

            <Row label='Live now'>
                {PEOPLE.map((person) => (
                    <button
                        key={person.userId}
                        type='button'
                        className='lab-chip'
                        aria-pressed={liveHandles.has(person.userId)}
                        onClick={() => toggleLive(person.userId, person.name)}
                    >{person.name}</button>
                ))}
                <button
                    type='button'
                    className='lab-chip'
                    title='The 24/7 channel: nobody’s stream, with a picture an admin uploaded (Remove it under Show › System Console › 24/7 channel pictures)'
                    aria-pressed={world.streams.some((item) => item.handle === RADIO_HANDLE)}
                    onClick={toggleRadio}
                >{'Lo-fi radio'}</button>
            </Row>

            {/* Twitch (design/twitch-live.md 11.3): the switches, who is live
                there, Victor's own connection, how Connect goes, and the
                hours the System Console shows. */}
            <Row label='Twitch'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.twitchSwitch}
                    title='Show people live on Twitch. Off is the default world: nothing new anywhere'
                    onClick={() => setTwitch((current) => {
                        current.twitchSwitch = !current.twitchSwitch;
                    })}
                >{'switch on'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.twitchEmbed}
                    title='Play Twitch streams inside the panel: off, a card opens twitch.tv (a toast here)'
                    onClick={() => setTwitch((current) => {
                        current.twitchEmbed = !current.twitchEmbed;
                    })}
                >{'player in panel'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.twitchPlayerBlocked}
                    title='This page cannot run Twitch’s player, as on a Mattermost served on :8065: the plate, and the reason for admins'
                    onClick={() => setTwitch((current) => {
                        current.twitchPlayerBlocked = !current.twitchPlayerBlocked;
                    })}
                >{'player blocked here'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={!world.twitchCredentials}
                    title='No Client ID or Client Secret: nothing Twitch shows, and an admin is told why'
                    onClick={() => setTwitch((current) => {
                        current.twitchCredentials = !current.twitchCredentials;
                    })}
                >{'no application'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.twitchCallbackProblem !== ''}
                    title='A Site URL on plain http: no callback address, so nobody can connect'
                    onClick={() => setTwitch((current) => {
                        current.twitchCallbackProblem = current.twitchCallbackProblem ? '' : 'Twitch only accepts an https:// callback address, and the Site URL is http://chat.example.com.';
                    })}
                >{'callback problem'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.twitchEmbedProblem !== ''}
                    title='The System Console’s check of the Site URL: Twitch’s player cannot run there'
                    onClick={() => setTwitch((current) => {
                        current.twitchEmbedProblem = current.twitchEmbedProblem ? '' : 'Twitch’s player only runs on the standard https port (443); this server uses :8065.';
                    })}
                >{'Site URL port'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.twitchProblem !== ''}
                    title='Twitch’s API down: the admin notice in the panel, and the System Console’s last check'
                    onClick={() => setTwitch((current) => {
                        current.twitchProblem = current.twitchProblem ? '' : 'Twitch could not be reached; the last good check was 6 minutes ago.';
                    })}
                >{'Twitch API down'}</button>
            </Row>

            <Row label='Live on Twitch'>
                {[{name: 'Erin', userId: 'u-erin'}, {name: 'Victor', userId: ME_ID}, {name: 'Caro', userId: 'u-carol'}].map((person) => (
                    <button
                        key={person.userId}
                        type='button'
                        className='lab-chip'
                        aria-pressed={isTwitchLive(world, person.userId)}
                        title={person.userId === ME_ID ? 'Victor live on Twitch (connecting him first if he is not)' : `${person.name} live on Twitch`}
                        onClick={() => toggleTwitchLive(person.userId)}
                    >{person.name}</button>
                ))}
                <button
                    type='button'
                    className='lab-chip'
                    title='Victor live on Broadcast Box and Twitch at once: one card, his Broadcast Box one, with Also on Twitch'
                    onClick={victorSimulcast}
                >{'Victor on both'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.streams.some((item) => item.userId === 'u-erin' && !item.alwaysOn)}
                    title='The simulcast handoff: Erin starts or stops her Broadcast Box stream. Both cards show for five seconds (the lab’s grace), then only the Broadcast Box one; a Twitch tile being watched keeps playing'
                    onClick={toggleErinBroadcastBox}
                >{'Erin on Broadcast Box'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Erin pauses her Broadcast Box stream for two seconds, shorter than the grace: nothing swaps'
                    onClick={pauseErinOnePoll}
                >{'Erin pauses a poll'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Watch Erin’s Twitch stream, as a card does: inline at 464 or wider, the theatre below'
                    onClick={() => {
                        const stream = snapshotOf().streams.find((item) => item.handle === 'tw-erin');
                        if (stream) {
                            watchOrOpen(stream, 'only');
                        } else {
                            labSay('Erin is not live on Twitch, or the switch is off.');
                        }
                    }}
                >{'watch Erin’s Twitch'}</button>
            </Row>

            <Row label='Victor’s Twitch'>
                {(['none', 'connected', 'lost', 'blocked'] as const).map((mode) => (
                    <button
                        key={mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={labVictorTwitchOf(world) === mode}
                        title={{
                            none: 'Not connected: the Twitch row of the relink key’s sheet offers Connect',
                            connected: 'Connected as VictorPlays: Disconnect on the Twitch row (and on the lock)',
                            lost: 'His account was proved by somebody else: the takeover notice over the bar',
                            blocked: 'An admin blocked him: nothing about Twitch shows',
                        }[mode]}
                        onClick={() => setVictorTwitch(mode)}
                    >{mode}</button>
                ))}
            </Row>

            <Row label='Connect'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.twitchTicket === 'refused'}
                    title='The ticket refused, as the limiter refuses it: the sentence in place of the buttons'
                    onClick={() => setTwitch((current) => {
                        current.twitchTicket = current.twitchTicket === 'ok' ? 'refused' : 'ok';
                    })}
                >{'ticket refused'}</button>
                {(['opens', 'blocked', 'desktop'] as const).map((mode) => (
                    <button
                        key={mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={world.twitchPopup === mode}
                        title={{
                            opens: 'A browser window opens and takes focus',
                            blocked: 'A pop-up blocker: nothing opens, and after four seconds the sheet says so',
                            desktop: 'The desktop app: its own window opens, window.open returns null, and there is no hint once it takes focus',
                        }[mode]}
                        onClick={() => setTwitch((current) => {
                            current.twitchPopup = mode;
                        })}
                    >{mode === 'desktop' ? 'desktop app' : `pop-up ${mode}`}</button>
                ))}
                {(['connects', 'fails', 'never'] as const).map((mode) => (
                    <button
                        key={mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={world.twitchWindow === mode}
                        title={{
                            connects: 'Twitch connects Victor 1.5 s later, and the server sends "me"',
                            fails: 'Twitch does not confirm: nothing is connected',
                            never: 'The window never answers: the sheet waits',
                        }[mode]}
                        onClick={() => setTwitch((current) => {
                            current.twitchWindow = mode;
                        })}
                    >{mode === 'never' ? 'never answers' : mode}</button>
                ))}
            </Row>

            <Row label='Twitch hours'>
                {(['ok', 'purging', 'setaside', 'halted'] as const).map((mode) => (
                    <button
                        key={mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={labTwitchLedgerOf(world) === mode}
                        onClick={() => setTwitchLedger(mode)}
                    >{mode === 'setaside' ? 'set-aside copy' : mode}</button>
                ))}
            </Row>

            <Row label='Add a plan'>
                <button
                    type='button'
                    className='lab-chip'
                    onClick={() => plan('Starting any second', 'Dave', 'u-dave', 2 * MINUTE_MS)}
                >{'in 2 min'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    onClick={() => plan('Arma co-op', 'Dave', 'u-dave', 45 * MINUTE_MS)}
                >{'in 45 min'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    onClick={() => plan('Film night', 'Bob', 'u-bob', 3 * HOUR_MS)}
                >{'in 3 hours'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    onClick={() => plan('Late one', 'Erin', 'u-erin', tomorrowEvening())}
                >{'tomorrow 20:00'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    onClick={() => plan('Sunday racing', 'Erin', 'u-erin', 4 * 24 * HOUR_MS)}
                >{'in 4 days'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    onClick={() => plan('Started an hour ago', 'Bob', 'u-bob', -1 * HOUR_MS)}
                >{'already started'}</button>
            </Row>

            <Row label='Planned'>
                {world.schedule.length === 0 ? <span className='lab-note'>{'nothing'}</span> : null}
                {world.schedule.map((item) => (
                    <button
                        key={item.id}
                        type='button'
                        className='lab-chip lab-chip-remove'
                        title={`Remove ${item.title}`}
                        onClick={() => unplan(item.id)}
                    >{`${item.who}: ${item.title} ✕`}</button>
                ))}
            </Row>

            <Row label='What’s on?'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={Boolean(world.nowPlaying['u-dave'] || world.nowPlaying['u-bob'])}
                    title='Dave has said Arma 3, linked to the real game, so it has art; Bob has said Elden Ring with lookups off, so it is a kind and words alone. They show while each of them is live.'
                    onClick={restoreNowPlaying}
                >{'Dave with art, Bob without'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Erin’s film night, linked to Heat, started half an hour ago: with Erin live and nothing said, her card shows the plan’s title'
                    onClick={erinFilmOn}
                >{'Erin’s plan is on'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={Boolean(world.nowPlaying[ME_ID])}
                    title='Victor has said he is on Hytale, which has a box-art poster: the footer bar’s What’s on? chip with its poster, beside the scissors, the link key and the calendar — the bar to look at at 320px. This remounts the panel.'
                    onClick={() => {
                        change((current) => {
                            const {[ME_ID]: _mine, ...others} = current.nowPlaying;
                            current.nowPlaying = current.nowPlaying[ME_ID] ? others : {...others, [ME_ID]: hytaleNowPlaying()};
                        });
                        labRemount();
                    }}
                >{'Victor: Hytale'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={Object.keys(world.nowPlaying).length === 0}
                    title='Nobody has said anything, Victor included: the footer’s bar shows the What’s on? television again'
                    onClick={clearAllNowPlaying}
                >{'clear'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={!world.me.media?.movies && !world.me.media?.games}
                    title='Looking titles up switched off by an admin: What’s on? still saves a kind and the words, with no cover, and art already saved is no longer relayed. This remounts the panel.'
                    onClick={() => change((current) => {
                        const off = Boolean(current.me.media?.movies || current.me.media?.games);
                        current.me = {...current.me, media: {movies: !off, games: !off, igdb: !off}};
                    })}
                >{'lookups off'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={Boolean(world.me.media?.games) && !world.me.media?.igdb}
                    title='Lookups on but no Twitch application set up: games come from Steam alone, the list says “Games on Steam”, and the Hytale plan’s art is not relayed, so it shows the stand-in. This remounts the panel.'
                    onClick={() => change((current) => {
                        const media = current.me.media;
                        const noIGDB = Boolean(media?.games) && !media?.igdb;
                        current.me = {...current.me, media: {movies: noIGDB ? Boolean(media?.movies) : true, games: true, igdb: noIGDB}};
                    })}
                >{'no IGDB'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.igdbSlow}
                    title='IGDB slower than a game search waits for: games come from Steam alone, and the list says IGDB did not answer (with nothing on Steam, “Nothing on Steam called …, and IGDB did not answer just now.”). The list asks once more a moment later, and the answer is kept only once it is complete.'
                    onClick={() => change((current) => {
                        current.igdbSlow = !current.igdbSlow;
                    })}
                >{'IGDB slow'}</button>
            </Row>

            {/* Remembered titles (media lookups): the System Console card's
                states. */}
            <Row label='Remembered titles'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.mediaCache.titles + world.mediaCache.searches + world.mediaCache.missing > 0}
                    title='The System Console card with something remembered: 12 titles and 40 searches, the oldest four weeks back'
                    onClick={() => change((current) => {
                        current.mediaCache = rememberedMediaCache();
                    })}
                >{'some'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.mediaCache.titles + world.mediaCache.searches + world.mediaCache.missing === 0}
                    title='Nothing ever remembered, so the card says “Nothing remembered yet.” and Forget has nothing to do. After a Forget it says “Nothing remembered.”'
                    onClick={() => change((current) => {
                        current.mediaCache = unusedMediaCache();
                    })}
                >{'nothing yet'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.mediaCacheForgetFails}
                    title='Forget answers the server’s 500: some of it could not be forgotten just now'
                    onClick={() => change((current) => {
                        current.mediaCacheForgetFails = !current.mediaCacheForgetFails;
                    })}
                >{'forget fails'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.mediaCache.picturesKept === false}
                    title='Mattermost’s file storage is not local (S3, for example): pictures stay in memory only, and the card says so instead of counting them'
                    onClick={() => change((current) => {
                        current.mediaCache = current.mediaCache.picturesKept === false ? rememberedMediaCache() : memoryOnlyMediaCache();
                    })}
                >{'memory only'}</button>
                <span className='lab-note'>{'the card is under Show › System Console, and counts when it opens or Refresh is pressed'}</span>
            </Row>

            {/* Who is let in without a token, and Unlink: the System
                Console's Access card's states. */}
            <Row label='Access card'>
                <AccessStates/>
            </Row>

            <Row label='Banner'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.me.bannerText !== ''}
                    title='The server’s Banner text: “Members only”, or none (a fresh server has none). Remembered in this browser, so a reload draws it until “me” answers'
                    onClick={() => change((current) => {
                        current.me = {...current.me, bannerText: current.me.bannerText === '' ? 'Members only' : ''};
                    })}
                >{'text'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.me.bannerColor !== ''}
                    title='The server’s Banner colour: #f2a900 (an amber that needs dark words), or none, when the banner is the theme’s accent with the words drawn on it'
                    onClick={() => change((current) => {
                        current.me = {...current.me, bannerColor: current.me.bannerColor === '' ? '#f2a900' : ''};
                    })}
                >{'colour'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.me.bannerPlacement === 'top'}
                    title='Banner placement set to Top only, one of the two choices Mattermost’s Classification Markings offer: the bar at the bottom of the panel goes. This remounts the panel.'
                    onClick={() => change((current) => {
                        current.me = {...current.me, bannerPlacement: current.me.bannerPlacement === 'top' ? 'both' : 'top'};
                    })}
                >{'top only'}</button>
            </Row>

            {/* What the server calls the plugin, and which artwork it has:
                changed by an admin, the server tells every page with the
                "me" event, which carries both. */}
            <Row label='Server name'>
                {LAB_NAMES.map((name) => (
                    <button
                        key={name}
                        type='button'
                        className='lab-chip'
                        aria-pressed={world.me.name === name}
                        title={`The server’s Name setting: “${name}” (${Array.from(name).length} characters). The panel’s title, the App Bar’s tooltip, the header button and the Settings tab follow it`}
                        onClick={() => labServerBrand({name})}
                    >{name.length > 20 ? `${Array.from(name).length} characters` : name}</button>
                ))}
                <button
                    type='button'
                    className='lab-chip'
                    title='Reload this page with the server unchanged, and its “me” three seconds late: until it answers, the title, the emblem, the ghost and the banner are what this browser remembered from the last answer (nothing blinks in on a server that has them)'
                    onClick={labReloadMeLate}
                >{'reload, “me” late'}</button>
            </Row>

            <Row label='Artwork'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.me.artwork.emblem}
                    title='An emblem uploaded: the watermark behind the panel and the lock’s emblem with the padlock on it. Without one the lock is the padlock alone'
                    onClick={() => labServerBrand({artwork: {...world.me.artwork, emblem: !world.me.artwork.emblem}})}
                >{'emblem'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.me.artwork.ghost}
                    title='A ghost uploaded: No Signal catches it now and then (often, in the lab). Without one, static only'
                    onClick={() => labServerBrand({artwork: {...world.me.artwork, ghost: !world.me.artwork.ghost}})}
                >{'ghost'}</button>
                <span className='lab-note'>{'the lab’s own stand-ins (lab/pictures)'}</span>
            </Row>

            {/* What system admins are newly told: the release in the
                panel's admin alerts (as an admin, unlocked),
                the release and the app check's verdicts on the System
                Console's App check card, which reads them as it opens; and
                push notifications' state on the Push notifications card,
                with Mattermost's own push notifications, whose being off is
                also an admin alert while the plugin's switch is on. */}
            {([
                ['Release', 'release', [
                    ['unsigned', 'unsigned', 'No release folder: a development build, or somebody’s own'],
                    ['signed', 'signed', 'A signed release whose files are all as signed'],
                    ['unlisted', 'files nobody signed', 'A signed release whose folder also holds 13 files nobody signed: an alert for admins'],
                    ['altered', 'altered', 'A release folder whose files are not as signed: an alert for admins'],
                    ['unreadable', 'cannot read', 'A signed release, and GET /release fails (a proxy answers 502): the App check card’s line says so'],
                ]],
                ['App check list', 'appCheck', [
                    ['some', 'some', 'Five people: verified, development, not verified, no longer accepted, an account that is gone'],
                    ['many', 'many', 'Forty-one people, so the list shows the newest twenty'],
                    ['empty', 'empty', 'No verdict that has not expired'],
                    ['off', 'check off', 'The App check setting is Off'],
                    ['problem', 'bad digest', 'A line of More signing certificates is not a digest'],
                    ['error', 'cannot read', 'The store does not answer'],
                    ['refused', 'refused', 'GET /admin/appcheck answers 403 forbidden, the server’s refusal for anybody not a system admin'],
                ]],
                ['Push status', 'push', [
                    ['some', 'some', 'Eleven phones of eight people at four push servers: working, waiting for the first push, failing for three hours, refused; and pushes not sent'],
                    ['empty', 'empty', 'On, and no phone registered'],
                    ['off', 'switch off', 'Send push notifications to Matty is off: no alert, whatever Mattermost says'],
                    ['unready', 'key not ready', 'On, and the server’s push key is not loaded yet'],
                    ['full', 'full', 'More than 5000 people: the counts are of the most recent'],
                    ['problem', 'bad host line', 'Two lines of Push servers on your own network are not a host or host:port'],
                    ['error', 'cannot read', 'The store does not answer: 503'],
                    ['refused', 'refused', 'GET /admin/push answers 403 forbidden, the server’s refusal for anybody not a system admin'],
                ]],
                ['Mattermost’s push', 'mattermostPush', [
                    ['on', 'on', 'Mattermost decides to push: no alert'],
                    ['off', 'off', 'Enable Push Notifications is “Do not send push notifications”: the card says so, and while the switch is on so does an admin alert in the panel'],
                    ['licence', 'needs a licence', 'Mattermost’s hosted push server without the licence it needs'],
                    ['unknown', 'cannot read', 'Mattermost’s configuration could not be read just now'],
                ]],
            ] as const).map(([row, field, options]) => (
                <Row
                    key={row}
                    label={row}
                >
                    {options.map(([value, words, note]) => (
                        <button
                            key={value}
                            type='button'
                            className='lab-chip'
                            aria-pressed={labAdminNews[field] === value}
                            title={note}
                            onClick={() => {
                                setLabAdminNews({[field]: value});
                                meHeard({data: {name: world.me.name, artwork: world.me.artwork}});
                            }}
                        >{words}</button>
                    ))}
                </Row>
            ))}

            <Row label='Airtime board'>
                <button
                    type='button'
                    className='lab-chip'
                    onClick={() => change((current) => {
                        current.allTime = [];
                        current.month = [];
                        current.allTimeTotal = 0;
                        current.monthTotal = 0;
                        current.monthMedals = [];
                    })}
                >{'empty'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    onClick={restoreBoards}
                >{'four streamers'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Both boards empty and the months already won kept, as after an import of past months alone: the trophy stays, with only the medals behind it'
                    onClick={pastMonthsOnly}
                >{'past months only'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={hasLongName(world)}
                    title='Erin with a display name wider than any column, on the boards and the medals'
                    onClick={toggleLongName}
                >{'long name'}</button>
            </Row>

            <Row label='Snoopers board'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.watchBoards && world.watchMonth.length + world.watchAllTime.length > 0}
                    title='Top Snoopers on, with hours watched this month and all time: the trophy card’s Snoopers tab'
                    onClick={restoreWatchBoards}
                >{'five snoopers'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.watchBoards && world.watchMonth.length + world.watchAllTime.length === 0}
                    title='Top Snoopers on and nobody counted yet: the tab says nobody has snooped'
                    onClick={() => emptyWatchBoards(true)}
                >{'empty'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={!world.watchBoards}
                    title='Top Snoopers switched off: no Snoopers tab, and no tabs at all, and nobody wears the binoculars'
                    onClick={() => emptyWatchBoards(false)}
                >{'off'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={topSnooperIn(world) === ME_ID}
                    title='Victor first on the all-time Snoopers board instead of Erin: the Top Snooper’s binoculars move to him, beside his crown in a post (the post below switches to his) and on his card when he is live'
                    onClick={() => {
                        // The post header goes with the binoculars to
                        // Victor, to show them beside his crown. Left on
                        // Erin it would go empty, and an empty header reads
                        // as the badge failing to render.
                        if (topSnooperIn(world) !== ME_ID) {
                            setPostBy(ME_ID);
                        }
                        toggleVictorSnoops();
                    }}
                >{'Victor snoops most'}</button>
            </Row>

            <Row label='Me'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.me.streamKey !== ''}
                    title='Victor with a stream linked, or not: planning, changing a plan and joining one need it. This remounts the panel; linking or unlinking from its own footer changes it in place.'
                    onClick={() => change((current) => {
                        current.me = {...current.me, streamKey: current.me.streamKey === '' ? 'live-victor' : ''};
                        current.linkSwitches += 1;
                    })}
                >{'linked'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.me.isAdmin}
                    onClick={() => change((current) => {
                        current.me = {...current.me, isAdmin: !current.me.isAdmin};
                    })}
                >{'system admin'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={!world.profile.isPublic}
                    onClick={() => change((current) => {
                        current.profile = {...current.profile, isPublic: !current.profile.isPublic};
                    })}
                >{'hidden by Broadcast Box'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={!world.profile.available}
                    title='Broadcast Box keeps no profile for Victor’s key, so his stream title cannot be set: the bar says why in place of the title, and the link key beside it is lit, since relinking is what fixes it. This remounts the panel.'
                    onClick={() => change((current) => {
                        current.profile = current.profile.available ?
                            {...current.profile, available: false, message: 'Broadcast Box has no reserved profile for your stream key.'} :
                            {...current.profile, available: true, message: undefined};
                    })}
                >{'title can’t be set'}</button>
            </Row>

            <Row label='Clips'>
                <button
                    type='button'
                    className='lab-chip'
                    title='The Clips dialog on its list, as the header’s clapper and the panel’s row open it'
                    onClick={() => openClips()}
                >{'open the list'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='The dialog on one clip by itself, as the trophies and a clip link open it: “that jump”, with a lead-in of 1.4 s, reported once. Escape closes it rather than going back.'
                    onClick={() => openClips('3f9a0c2e7b1d4a55')}
                >{'open “that jump”'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='The list with Yours on: clips Victor made and clips of his stream'
                    onClick={() => openClips(undefined, 'yours')}
                >{'open yours'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.clips.length > 0}
                    title='The six sample clips: one by Victor, one of his stream, one going in two days, one with an 80-character title, one with no picture or title, and “that jump”'
                    onClick={restoreClips}
                >{'6 clips'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.clips.length === 0}
                    title='No clips: the empty list, which says how one is made, or that somebody has to be live first'
                    onClick={clearClips}
                >{'no clips'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={!world.clipsEnabled}
                    title='Clips switched off in the plugin settings: published clips stay listed, and the empty list says clipping is off'
                    onClick={() => change((current) => {
                        current.clipsEnabled = !current.clipsEnabled;
                    })}
                >{'clipping off'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.drafts.length > 0}
                    title='Two drafts of Victor’s: one with 52 minutes left, and one being published, which has no buttons. The clapper counts them'
                    onClick={() => {
                        setLabDrafts(world.drafts.length === 0);
                        refreshDrafts();
                    }}
                >{'two drafts'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.downloadsAllowed}
                    title='3.2.1: Let people download clips, the admin’s switch, off as it ships. On: Download in the viewer for Victor while linked, and the second switch in Clips of your stream. The open viewer asks about its clip again as the clips’ version moves'
                    onClick={() => change((current) => {
                        current.downloadsAllowed = !current.downloadsAllowed;
                    })}
                >{'downloads on'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.streamerNoDownloads}
                    title='3.2.1: Victor turned off downloads of his stream’s clips, as his own switch does: no Download on them for anybody, whatever the admin’s switch says'
                    onClick={() => change((current) => {
                        current.streamerNoDownloads = !current.streamerNoDownloads;
                    })}
                >{'Victor blocks downloads'}</button>
            </Row>

            <Row label='Access'>
                {([
                    ['linked', 'linked', 'Victor unlocked the panel with his bearer token: the whole panel. As a fresh page would know him: no lock line, nothing waiting'],
                    ['letIn', 'let in', 'A system admin let Victor in without a token: the watch-only panel — Up next read-only with × on his own plan (every plan while he is an admin), no scissors, no Pop out, the Clips dialog with nothing to act with, and WATCH ONLY in the footer with Link your stream'],
                    ['locked', 'locked', 'Neither: the lock screen, and nothing of the panel behind it. The header is the wordmark alone, and no small player ever shows'],
                    ['unknown', 'unknown', '“me” could not be read on a fresh page: the lock’s NO SIGNAL face with Try again, never LOCKED. (A linked page keeps its panel: see Store blip below)'],
                    ['starting', 'starting', '“me” held for 2 s on a fresh page: the lock frame, black for 250 ms and then the emblem at a quarter, until it comes'],
                    ['later', 'signed in after load', 'The page loaded on Mattermost’s sign-in page with nobody signed in, and Victor signs in three seconds later without it loading again: “me” and the snapshot are asked for as the store says who signed in'],
                    ['away', 'signed out, back locked', 'Victor signs out in place with tiles playing and the Clips dialog open, and somebody locked signs in three seconds later on the same page: every stream stops, the watch list and anything waiting are forgotten, and their own “me” decides the rest'],
                ] as const).map(([mode, label, note]) => (
                    <button
                        key={mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={accessMode === mode}
                        title={note}
                        disabled={switches.signingIn && mode !== accessMode}
                        onClick={() => {
                            if (mode === 'later' || mode === 'away') {
                                signInAfterLoad(mode);
                            } else {
                                freshPage(mode);
                            }
                        }}
                    >{label}</button>
                ))}
                <span className='lab-note'>{`server: ${labAccessOf(world)}${switches.held ? ', the page not told yet' : ''}`}</span>
            </Row>

            <Row label='Access changes'>
                <button
                    type='button'
                    className='lab-chip'
                    title='Let in → let out while watching: the server starts refusing with locked, and a tile’s next request carries it to the page (the refusal listener), which reads “me” again: tiles stop, and the lock says “You are no longer let in.” With Socket down, nothing carries it until Reconnect or Clock +60 s'
                    onClick={letOutNow}
                >{'Let out now'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='While the lock is showing: an admin lets Victor in, and the "me" event arrives (the same handler the WebSocket calls). The hidden “An admin let you in to watch.” and the unlock'
                    onClick={letInNow}
                >{'Let in now'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Victor linked AND on the let-in list, with Dave playing and the clip editor open on a draft: the set-up for the next chip'
                    onClick={linkedLetInEditing}
                >{'linked + let in, editing'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Linked → let in: his link is undone elsewhere, and the "me" event arrives. The editor closes, drafts go, the panel remounts watch-only, and the streams keep playing'
                    onClick={unlinkWhileLetIn}
                >{'Unlink while let in'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Victor unlocks in another window: the server says linked and this window hears nothing. Press Window focused to see it here'
                    onClick={unlockedElsewhere}
                >{'Unlocked elsewhere'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Dispatches focus on the window and visibilitychange on the document, as looking at a popped-out window again does (the panel reads “me” again, at most once in 10 s). A button, because a scripted focus() fires no focus events while the lab’s window is unfocused'
                    onClick={windowFocused}
                >{'Window focused'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={switches.socketDown}
                    title='The WebSocket is down: "me" events are dropped, and Let out now carries nothing to the page'
                    onClick={() => flipSwitch('socketDown')}
                >{'Socket down'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='The WebSocket reconnects: the reconnect handler reads “me” again (an event sent while it was down may never come)'
                    onClick={() => {
                        setSwitch('socketDown', false);
                        socketReconnected();
                        labSay('Reconnected: “me” read again, as registerReconnectHandler does.');
                    }}
                >{'Reconnect'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='The store does not answer the next “me” (access: unknown), which is then asked for: a linked page keeps the panel, its tiles and a quiet notice, and reads “me” once more 5 s later; a fresh page shows NO SIGNAL'
                    onClick={storeBlip}
                >{'Store blip'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='An upvote sent through the client as if the panel had offered one (it offers none to somebody let in): refused with watch_only, which the client tells the page, and “me” is read again'
                    onClick={() => {
                        voteClip('3f9a0c2e7b1d4a55', true).then(() => labSay('The vote went through: Victor is linked.'), (reason: unknown) => {
                            const code = (reason as {code?: string})?.code || 'network';
                            labSay(`The vote was refused with ${code}; the page read “me” again (counters: identity refresh).`);
                        });
                    }}
                >{'send a vote'}</button>
            </Row>

            <Row label='Lab clock'>
                <button
                    type='button'
                    className='lab-chip'
                    title='Fifteen seconds on: a revealed bearer token hides itself, and a removed let-in row’s Undo lapses'
                    onClick={() => advanceClock(15000)}
                >{'+15 s'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='A minute on: the re-read of “me” while anything plays (with the world let out silently, it locks)'
                    onClick={() => advanceClock(60000)}
                >{'+60 s'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Ten minutes on: a stream or a clip waiting for the lock lapses, and the lock’s line goes'
                    onClick={() => advanceClock(600000)}
                >{'+10 min'}</button>
            </Row>

            <Row label='Lock screen'>
                {LINK_ANSWERS.map(([answer, label, note]) => (
                    <button
                        key={answer}
                        type='button'
                        className='lab-chip'
                        aria-pressed={world.linkAnswer === answer}
                        title={`What Unlock gets next: ${note}`}
                        onClick={() => change((current) => {
                            current.linkAnswer = answer;
                        })}
                    >{label}</button>
                ))}
            </Row>

            <Row label='Lock words'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={!world.configured}
                    title='The plugin not set up: no bar and no Unlock, one sentence saying so, the same for a system admin'
                    onClick={() => change((current) => {
                        current.configured = !current.configured;
                    })}
                >{'not set up'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.everyAlert}
                    title='Every admin alert at once (let-in list, voice, birthdays, airtime), for a system admin: in the panel once it is unlocked, and never on the lock, which is the same for an admin as for anybody'
                    onClick={() => change((current) => {
                        current.everyAlert = !current.everyAlert;
                    })}
                >{'every admin alert'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.publicWatch}
                    title='A Public watch URL is set: linked people get Pop out on a tile, a plain link through the plugin; let-in people never do'
                    onClick={() => change((current) => {
                        current.publicWatch = !current.publicWatch;
                    })}
                >{'public watch URL'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={switches.slowUnlock}
                    title='The unlock ten times slower: both padlocks swinging open, the glass over the emblem clearing, the fade, then the panel’s boot sweep'
                    onClick={() => {
                        const slow = !labSwitches.slowUnlock;
                        setSwitch('slowUnlock', slow);
                        slowLockOpening(slow ? 10 : 1);
                    }}
                >{'slow unlock'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={switches.rhsFocus}
                    title='Mattermost focuses the side panel as it opens from closed: two timer hops, then the first focusable thing in it (the header’s Expand), as sidebar_right.tsx does from 10.11. Not when the panel takes the place of an open thread or search: focus then stays on the icon pressed. Without it, the bar taking focus would pass trivially'
                    onClick={() => flipSwitch('rhsFocus')}
                >{'Mattermost focuses the side panel'}</button>
                {(['main', 'channel-popout', 'other-popout'] as const).map((value) => (
                    <button
                        key={value}
                        type='button'
                        className='lab-chip'
                        aria-pressed={kind === value}
                        title='Which window this is, for a shared clip’s Watch and the lock’s openings: a thread popout has no side panel, so a locked reader’s card says where to unlock'
                        onClick={() => {
                            setKind(value);
                            pretendWindowKind(value === 'main' ? null : value);
                            change(() => undefined);
                        }}
                    >{{main: 'main window', 'channel-popout': 'channel popout', 'other-popout': 'thread popout'}[value]}</button>
                ))}
            </Row>

            <Row label='Admin'>
                {([
                    ['no', 'no', 'Victor is not a system admin: no clips alert in the panel, and in the System Console the Clips list refuses him (in Mattermost he could not open it at all)'],
                    ['linked', 'linked', 'A system admin with his stream linked: the clips alert in the panel, the whole of Clips in the System Console, and Clips in the panel'],
                    ['unlinked', 'not linked', 'A system admin who has not linked a stream: the whole of Clips in the System Console all the same (Victor’s answer to question A), and the clips alert, while the panel’s Clips stay locked for him'],
                ] as const).map(([mode, label, note]) => (
                    <button
                        key={mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={adminMode === mode}
                        title={note}
                        onClick={() => change((current) => {
                            const streamKey = {no: current.me.streamKey, linked: 'live-victor', unlinked: ''}[mode];
                            if (streamKey !== current.me.streamKey) {
                                current.linkSwitches += 1;
                            }
                            current.me = {...current.me, isAdmin: mode !== 'no', streamKey};
                        })}
                    >{label}</button>
                ))}
            </Row>

            <Row label='Best Clipper'>
                {([
                    ['none', 'none', 'Nobody holds it: an admin reset every score, so no upvote counts yet. No clapperboard anywhere, and the Clippers tab says nobody holds it'],
                    ['bob', 'Bob', 'Bob, with 142 upvotes on 9 clips: the clapperboard beside his name in a post (Post by: Bob) and on his profile, and the Clippers tab'],
                    ['victor', 'Victor (me)', 'Victor, with 180 banked: his post shows the crown, then the binoculars when he snoops most, then the clapperboard. Only while he is linked: unlink him and Bob has it back'],
                ] as const).map(([who, label, note]) => (
                    <button
                        key={who}
                        type='button'
                        className='lab-chip'
                        aria-pressed={who === 'none' ? !world.bestClipper : world.bestClipper?.userId === (who === 'bob' ? 'u-bob' : ME_ID)}
                        title={note}
                        onClick={() => setLabBestClipper(who)}
                    >{label}</button>
                ))}
            </Row>

            <Row label='Recording'>
                {([
                    ['warming', 'warming 0:03', 'Every live stream has been recorded for three seconds: the scissors are greyed, “Clipping starts in a few seconds”, and wake at five with 0:05 in their corner, counting up'],
                    ['partial', 'partial 0:47', 'Recorded for 47 seconds: the scissors clip what there is, with 0:47 in their corner counting up to the whole length, and the draft says only that much had been recorded'],
                    ['truncated', 'truncated', 'Recording, at a bitrate too high for the whole length to fit: a press gives 1:24, and the editor says why'],
                    ['ready', 'ready', 'Recording, with the whole length held: “Clip the last 2 minutes”, C included'],
                    ['failed', 'failed', 'The recorder cannot reach the stream: “Clipping isn’t working on this stream just now”, greyed'],
                    ['unsupported', 'unsupported', 'A stream the recorder cannot cut (AV1, or B-frames): greyed, and says so'],
                    ['full', 'full', 'No room left on the server for recording: greyed, and says so'],
                    ['queued', 'queued', 'Every recorder slot is busy with other streams: greyed, and says so'],
                    ['off', 'off', 'The streamer switched clipping of their stream off: “Dave has switched clipping off”, greyed'],
                    ['dropped', 'dropped (tail)', 'Every live stream has just come back after a drop: its new recording is starting, and the end before the drop is held for ten minutes, so the scissors clip that — “Clip the last 2:00 before the stream dropped”, and the editor’s line ends “before it dropped”'],
                ] as const).map(([mode, label, note]) => (
                    <button
                        key={mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={world.recording === mode}
                        title={`${note}. Dave goes live and is watched, so his tile has the scissors; point at it to see them.`}
                        onClick={() => {
                            watchDave();
                            setLabRecording(mode);
                        }}
                    >{label}</button>
                ))}
            </Row>

            <Row label='Capture'>
                {([
                    ['ok', 'ok', 'The draft is cut as the server would'],
                    ['slow', 'slow', 'Three seconds to cut: the editor’s CUTTING plate, with the title already taking typing'],
                    ['fails', 'fails', 'The recording could not be cut (503 recording_failed): Try again'],
                    ['rate', 'rate-limited', 'Too many presses in a row (429)'],
                    ['cap', '3 drafts', 'Victor already has three drafts (409 draft_cap): Open drafts'],
                    ['expired', 'draft expired', 'The draft is cut and has expired before it can be played or published: the editor finds out from its file, and says so'],
                    ['not_watching', 'not watching', 'Victor is not watching that stream as far as the server knows (403 not_watching)'],
                    ['forgotten', 'server forgot', 'The server restarted (the plugin was disabled and enabled, or Mattermost restarted) and has no record of anybody watching: the first press is refused (403 not_watching), the panel tunes that tile in again behind the editor, and the second press cuts. Pressing this forgets everybody again. With Dave already watched, ✂ clip Dave now shows it; a tile tuned in after the press was heard, and cuts at once'],
                    ['wiped', 'wiped', 'The streamer wiped what was held just after the cut: the editor finds out from its file, and says the draft is gone'],
                ] as const).map(([mode, label, note]) => (
                    <button
                        key={mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={world.captureMode === mode}
                        title={note}
                        onClick={() => {
                            if (mode === 'forgotten') {
                                forgetViewers();
                            }
                            change((current) => {
                                current.captureMode = mode;
                            });
                        }}
                    >{label}</button>
                ))}
                <button
                    type='button'
                    className='lab-chip'
                    title='Dave live and watched, and his scissors pressed: the editor opens on the last two minutes of his stream, as a press of ✂ or C on his tile does'
                    onClick={() => {
                        const handle = watchDave();
                        if (handle) {
                            startClip({handle, title: 'Dave'}, 'live', getWorld().clipLength, null);
                        }
                    }}
                >{'✂ clip Dave now'}</button>
            </Row>

            <Row label='Publish'>
                {([
                    ['ok', 'ok', 'Published, with the picture taken from half a second in'],
                    ['slow', 'slow (12 s)', 'Twelve seconds to publish: the bar under the picture, and after ten seconds “Still working — longer clips take a moment.”'],
                    ['fails', 'fails', 'The server could not save it (503 store): the draft is still here, Try again'],
                    ['full', 'storage full', 'The server is out of room for clips (507 storage_full)'],
                    ['share_full', 'share full', 'Victor is using his share of clip space (507 user_storage_full): Your clips'],
                    ['duplicate', 'duplicate', 'Most of this moment is already published (409 duplicate): Open Clips'],
                    ['network_then_ok', 'network then ok', 'Published, and the answer lost on the way back: “Checking whether it was published…”, and the clip is found two seconds later'],
                    ['expired', 'draft expired', 'The draft expired before it was published (410 draft_gone)'],
                ] as const).map(([mode, label, note]) => (
                    <button
                        key={mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={world.publishMode === mode}
                        title={note}
                        onClick={() => change((current) => {
                            current.publishMode = mode;
                        })}
                    >{label}</button>
                ))}
            </Row>

            <Row label='Stream tag'>
                {([
                    ['hytale', 'Dave: Hytale', 'Dave says he is on Hytale, a game Steam does not sell, which IGDB found (igdb:113360): a clip of his carries it, in the editor’s line, on its card and in the viewer, where its line opens the game’s profile with IGDB’s link'],
                    ['heat', 'Dave: Heat', 'Dave says he is on Heat, a movie: the clip carries a movie'],
                    ['none', 'none', 'Dave says nothing and has no plan on: a clip of his carries no tag, and nothing is shown for one'],
                ] as const).map(([tag, label, note]) => (
                    <button
                        key={tag}
                        type='button'
                        className='lab-chip'
                        aria-pressed={labTagOf(world) === tag}
                        title={note}
                        onClick={() => {
                            watchDave();
                            setDaveTag(tag);
                        }}
                    >{label}</button>
                ))}
            </Row>

            <Row label='Sound'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.noSound}
                    title='Safari, which plays the pictures of a clip but not its Opus sound: the editor and the viewer say “This browser will play this clip without sound.”, play it silent and leave the stream its sound'
                    onClick={() => change((current) => {
                        current.noSound = !current.noSound;
                    })}
                >{'Safari no sound'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={switches.stealKeys}
                    title='Mattermost’s own habit: a printable key pressed anywhere but in a text field moves focus to the message box and types there (use_textbox_focus.tsx). On, C on a tile, Space, I and O in the editor, and any letter in one of the plugin’s dialogs must never land in the message box under the panel'
                    onClick={() => flipSwitch('stealKeys')}
                >{'Mattermost takes stray keys'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Ctrl+V as a Russian keyboard sends it: the letter м, keyCode 86. Mattermost still sees a paste (it goes by the code when the letter is another) and takes it to the message box; on the neighbours of the panel’s token bar (Unlock, Show, Try again, Save, Link) it must land in the bar. Pressed onto whatever has focus, which the chip does not take: put focus there with Tab first. A script cannot paste, so it says where focus ended'

                    // Not a focus thief: the key goes to whatever had focus.
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => {
                        const target = (document.activeElement as HTMLElement | null) || document.body;
                        const from = labDescribe(target);
                        target.dispatchEvent(new KeyboardEvent('keydown', {key: 'м', code: 'KeyV', keyCode: 86, which: 86, ctrlKey: true, bubbles: true, cancelable: true}));
                        labSay(`Ctrl+V, Russian keyboard, pressed on ${from}: focus is now on ${labDescribe(document.activeElement as HTMLElement | null)}.`);
                    }}
                >{'Ctrl+V, Russian keyboard'}</button>
            </Row>

            <Row label='Clip fetch'>
                {([
                    ['ok', 'ok', 'Every clip request answered at the lab’s usual speed'],
                    ['slow', 'slow', 'Three seconds for the list, for one clip and for the shared clips’ cards: the loading cards, the viewer waiting, and a shared clip’s card breathing (state 6; press “cards ask again” under Shared clips)'],
                    ['fails', 'fails (503)', 'The list, one clip and the shared clips’ cards refused with 503 store: the error with Retry, and a card saying it could not check (state 8)'],
                    ['network', 'network', 'No answer at all for the list, one clip and the shared clips’ cards'],
                    ['gone', 'gone (410)', 'Every clip asked for by itself has gone: the viewer says so, the Trophies’ best clip line goes, and every linked reader’s shared clip card says the clip has gone (state 7). The list is fetched as usual.'],
                ] as const).map(([mode, label, note]) => (
                    <button
                        key={mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={world.clipFetch === mode}
                        title={note}
                        onClick={() => change((current) => {
                            current.clipFetch = mode;
                        })}
                    >{label}</button>
                ))}
            </Row>

            <Row label='Clip file'>
                {([
                    ['ok', 'ok', 'The sample clip’s file, at once'],
                    ['hang', 'never comes', 'A clip’s file that never arrives: the player’s loading, the tuning burst saying LOADING over the clip’s title. Choose it, then open a clip; in the System Console too'],
                    ['fail', 'fails', 'A clip’s file the browser cannot read: CAN’T PLAY on static, and why with Try again on the line under the picture. Choose it, then open a clip; in the System Console too'],
                ] as const).map(([mode, label, note]) => (
                    <button
                        key={mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={world.clipFile === mode}
                        title={note}
                        onClick={() => change((current) => {
                            current.clipFile = mode;
                        })}
                    >{label}</button>
                ))}
            </Row>

            <Row label='Ended tail'>
                {([
                    ['none', 'none', 'No stream has stopped with its end still held: no Just ended'],
                    ['ended', 'Dave went off 2 min ago', 'Dave is watched and goes off the air: his tile waits, with the scissors — “Clip the last 2:00 before Dave went off” — until it gives up; the dialog’s Just ended says “Dave went off 2 min ago — the last 2:00 is held for 8 more minutes.” with Clip the end. The editor’s line ends “before it went off”, and the draft carries what Dave was on'],
                    ['dropped', 'Bob dropped 40 s ago', 'Bob’s stream dropped forty seconds ago and has not come back: Just ended says “Bob’s stream dropped 40 s ago — the last 0:47 is held…” with Clip before the drop, and a tile of his, if he was watched (2×2 grid), waits with “Clip the last 0:47 before the stream dropped”'],
                ] as const).map(([kind, label, note]) => (
                    <button
                        key={kind}
                        type='button'
                        className='lab-chip'
                        aria-pressed={kind === 'none' ? world.tails.length === 0 : world.tails.some((tail) => tail.reason === kind)}
                        title={`${note}. The System Console lists it under Held after the end, with Wipe.`}
                        onClick={() => {
                            // Dave watched first, and seen live by the panel
                            // for a moment, as somebody watching him would
                            // have; then he goes.
                            if (kind === 'ended' && !getWatching().includes(ensureDaveLive())) {
                                watchDave();
                                window.setTimeout(() => setLabTail(kind), 800);

                                return;
                            }
                            setLabTail(kind);
                        }}
                    >{label}</button>
                ))}
            </Row>

            <Row label='Streamer'>
                {([
                    ['not-linked', 'not linked', 'Victor has not linked his stream, and nothing matches it to him: no notice and no switch, only Link Your Account'],
                    ['username', 'username-attributed', 'Victor has not linked his stream, but it is live and matched to him by his username, so it is kept for clipping all the same: the read-only notice above Link Your Account, which is the way to switch that off or wipe it'],
                    ['allowed', 'allowed', 'Victor linked by hand, clips of his stream allowed: “Clips are on…” while he is live, “Your stream has stopped: its last 2:00 is held here…” while the end of it is held after he goes off, and “When you’re live…” otherwise; the switch on, Wipe the last 2:00 while there is something held, and “N clips of your stream · View” once the list has been fetched'],
                    ['off', 'off', 'Victor linked by hand, clipping of his stream switched off: “Clipping of your stream is off.”, the switch off, no Wipe; his tile’s scissors say he has switched clipping off'],
                ] as const).map(([mode, label, note]) => (
                    <button
                        key={mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={labStreamerOf(world) === mode}
                        title={`${note}. The open panel follows, as it follows “me”.`}
                        onClick={() => setStreamer(mode)}
                    >{label}</button>
                ))}
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={liveHandles.has(ME_ID)}
                    title='Victor on the air or off it, as Live now › Victor does: going off holds the end of his stream, which Wipe still reaches for ten minutes'
                    onClick={() => toggleLive(ME_ID, 'Victor')}
                >{'Victor live'}</button>
            </Row>

            <Row label='Unseen clips'>
                <button
                    type='button'
                    className='lab-chip'
                    title='Victor last looked six hours ago: the clapper’s dot, and the two newest clips NEW in the list'
                    onClick={() => resetClipsSeen(Date.now() - (6 * HOUR_MS))}
                >{'yes: 2 new'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Victor has seen every clip: no dot and no NEW'
                    onClick={() => resetClipsSeen(Date.now())}
                >{'no: all seen'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Victor has never opened the list: the dot and NEW on the row say there are clips to see, and nothing in the list is marked new, there being nothing to compare with'
                    onClick={() => resetClipsSeen(0)}
                >{'never looked'}</button>
            </Row>

            <Row label='Retention pending'>
                {[0, 41].map((count) => (
                    <button
                        key={count}
                        type='button'
                        className='lab-chip'
                        aria-pressed={world.retentionPending === count}
                        title={count === 0 ? 'Nothing waits to be removed' : '“Keep clips for” was just lowered: 41 clips go when its day of grace ends. The panel’s clips alert says so to admins, and the System Console says when'}
                        onClick={() => change((current) => {
                            current.retentionPending = count;
                        })}
                    >{String(count)}</button>
                ))}
            </Row>

            <Row label='Layout'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={hasLongClipTitle(world)}
                    title='“that jump”, the most upvoted clip, with an 80-character title: the Trophies’ best clip line, the list, the viewer and the System Console all have to give way'
                    onClick={toggleLongClipTitle}
                >{'long title'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='Victor, Dave, Bob and Erin live and all four watched, two by two: the set glides when the Up next row moves above it or goes'
                    onClick={() => {
                        everybodyLive();
                        getWorld().streams.filter((item) => !item.alwaysOn).forEach((item) => addWatching(item.handle));
                    }}
                >{'2×2 grid'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    title='The four of them watched two by two, with every scissors that clips an end: Victor, Bob and Erin have just come back after a drop (“Clip the last 2:00 before the stream dropped”), and Dave has just gone off, his tile waiting with “Clip the last 2:00 before Dave went off”. Point at each tile: the row has to fit a quarter of the panel'
                    onClick={() => {
                        everybodyLive();
                        getWorld().streams.filter((item) => !item.alwaysOn).forEach((item) => addWatching(item.handle));

                        // Seen live by the panel first, so Dave's waiting tile
                        // still has his face.
                        window.setTimeout(everybodyLiveWithTails, 800);
                    }}
                >{'2×2 with tails'}</button>
            </Row>

            <Row label='Live now'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.streams.some((item) => item.paused)}
                    title='A paused publisher is still listed by Broadcast Box'
                    disabled={world.streams.length === 0}
                    onClick={() => change((current) => {
                        const pausing = !current.streams.some((item) => item.paused);
                        current.streams = current.streams.map((item) => ({...item, paused: pausing}));
                    })}
                >{'paused'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.streams.some((item) => item.alwaysOn)}
                    disabled={world.streams.length === 0}
                    onClick={() => change((current) => {
                        const on = !current.streams.some((item) => item.alwaysOn);
                        current.streams = current.streams.map((item) => ({...item, alwaysOn: on}));
                    })}
                >{'24/7 channel'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={!world.livePreviews}
                    title='The "Show live preview pictures" setting, off'
                    onClick={() => change((current) => {
                        current.livePreviews = !current.livePreviews;
                    })}
                >{'previews off'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    disabled={world.streams.length === 0}
                    title='The first stream being watched (or else the first live one) drops off the list for five seconds and comes back under the same handle, as OBS reconnecting does'
                    onClick={() => {
                        const handle = getWatching()[0] || world.streams[0]?.handle;
                        if (handle) {
                            dropFor(handle, 5000);
                        }
                    }}
                >{'OBS reconnect'}</button>
            </Row>

            <Row label='In voice'>
                {['Victor', 'Dave', 'Bob', 'Erin'].map((name) => (
                    <button
                        key={name}
                        type='button'
                        className='lab-chip'
                        aria-pressed={world.voice.some((person) => person.name === name)}
                        disabled={!world.voiceConnected}
                        onClick={() => change((current) => {
                            current.voice = current.voice.some((person) => person.name === name) ?
                                current.voice.filter((person) => person.name !== name) :
                                current.voice.concat({name, userId: `u-${name.toLowerCase()}`});
                        })}
                    >{name}</button>
                ))}
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.voice.some((person) => person.muted)}
                    disabled={world.voice.length === 0}
                    onClick={() => change((current) => {
                        const muting = !current.voice.some((person) => person.muted);
                        current.voice = current.voice.map((person) => ({...person, muted: muting}));
                    })}
                >{'muted'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={!world.voiceConnected}
                    title='The plugin cannot reach Mumble: the row says nothing rather than claiming voice is empty'
                    onClick={() => change((current) => {
                        current.voiceConnected = !current.voiceConnected;
                    })}
                >{'Mumble unreachable'}</button>
            </Row>

            <Row label='Server'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.failWrites}
                    onClick={() => change((current) => {
                        current.failWrites = !current.failWrites;
                    })}
                >{'writes fail'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.latencyMs > 600}
                    onClick={() => change((current) => {
                        current.latencyMs = current.latencyMs > 600 ? 180 : 1400;
                    })}
                >{'slow'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={!world.configured}
                    onClick={() => change((current) => {
                        current.configured = !current.configured;
                    })}
                >{'not configured'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.error !== ''}
                    onClick={() => change((current) => {
                        current.error = current.error === '' ? 'Broadcast Box did not answer.' : '';
                    })}
                >{'origin down'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={tuning}
                    title='Take four seconds to find the picture, so the channel-change effect can be looked at'
                    onClick={() => {
                        const next = tuning ? 0 : 4000;

                        setTuneDelay(next);
                        onTuning(next > 0);
                    }}
                >{'slow to tune'}</button>
            </Row>

            <Row label='Hashtag search'>
                {TAG_SEARCH.map((option) => (
                    <button
                        key={option.mode}
                        type='button'
                        className='lab-chip'
                        aria-pressed={world.tagSearch === option.mode}
                        title={option.note}
                        onClick={() => change((current) => {
                            current.tagSearch = option.mode;
                        })}
                    >{option.label}</button>
                ))}
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.mainWindowGone}
                    title='From the popout window: the Mattermost window it came from has closed, so nobody answers its search'
                    onClick={() => change((current) => {
                        current.mainWindowGone = !current.mainWindowGone;
                    })}
                >{'popout’s window gone'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={phoneSized}
                    title='Mattermost in its phone layout, where the side panel covers the screen: opening a message closes the panel first'
                    onClick={() => labDispatch({type: 'BROWSER_WINDOW_RESIZED', data: phoneSized ? 'desktopView' : 'mobileView'})}
                >{'phone-sized'}</button>
            </Row>

            <Row label='Show'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={!showingConsole}
                    onClick={() => onConsole(false)}
                >{'The panel'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={showingConsole}
                    onClick={() => onConsole(true)}
                >{'System Console'}</button>
            </Row>

            <Row label='React root'>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={!CONCURRENT_ROOT}
                    title='As Mattermost 11.11 mounts its page by default: React 18 through ReactDOM.render, which batches updates as React 17 did. Reloads the lab.'
                    onClick={() => remountAs(false)}
                >{'legacy (Mattermost’s default)'}</button>
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={CONCURRENT_ROOT}
                    title='As a server with the EnableConcurrentReact feature flag mounts it: createRoot, with React 18’s automatic batching. Reloads the lab.'
                    onClick={() => remountAs(true)}
                >{'concurrent (feature flag)'}</button>
            </Row>

            <Row label='Everything'>
                <button
                    type='button'
                    className='lab-chip lab-chip-reset'
                    onClick={reset}
                >{'Reset the lab'}</button>
                <span className='lab-note'>{`times read in ${Intl.DateTimeFormat().resolvedOptions().timeZone}`}</span>
            </Row>

            <h2 className='lab-subtitle'>{'Elsewhere in Mattermost'}</h2>
            <p className='lab-blurb'>
                {'The two places the plugin renders inside Mattermost’s own furniture, on the same fake data.'}
            </p>

            <Row label='Search bar'>
                <SearchBar/>
            </Row>

            <Row label='Post by'>
                {PEOPLE.map((person) => (
                    <button
                        key={person.userId}
                        type='button'
                        className='lab-chip'
                        aria-pressed={postBy === person.userId}
                        onClick={() => setPostBy(person.userId)}
                    >{person.name}</button>
                ))}
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={lightPost}
                    title='The post on one of Mattermost’s light themes, where the badges sit on white'
                    onClick={() => setLightPost(!lightPost)}
                >{'light theme'}</button>
            </Row>

            <div
                className='lab-post'
                data-theme={lightPost ? 'light' : undefined}
            >
                <span className='lab-post-name'>{PEOPLE.find((person) => person.userId === postBy)?.name}</span>
                <span className='lab-post-badges'>
                    <PostAwardBadges post={{user_id: postBy}}/>
                </span>
                <span className='lab-post-time'>{'19:42'}</span>
            </div>
            <div
                className='lab-post-body'
                data-theme={lightPost ? 'light' : undefined}
            >{'Going live in ten.'}</div>

            <Row label='Profile'>
                {PEOPLE.map((person) => (
                    <button
                        key={person.userId}
                        type='button'
                        className='lab-chip'
                        aria-pressed={profileFor === person.userId}
                        onClick={() => setProfileFor(person.userId)}
                    >{person.name}</button>
                ))}
                <button
                    type='button'
                    className='lab-chip'
                    aria-pressed={lightPopover}
                    title='The popover on one of Mattermost’s light themes'
                    onClick={() => setLightPopover(!lightPopover)}
                >{'light theme'}</button>
            </Row>

            <div
                className='lab-popover'
                data-theme={lightPopover ? 'light' : undefined}
            >
                <div className='lab-popover-name'>{PEOPLE.find((person) => person.userId === profileFor)?.name}</div>
                <PopoverLive user={{id: profileFor}}/>
                {/* Mattermost's actions slot, which it draws in your own
                    popover too: the takebacks (design/3.2.0.md 4.6). */}
                <PopoverTakebacks
                    key={profileFor}
                    user={{id: profileFor}}
                />
            </div>

            <h2 className='lab-subtitle'>{'Shared clips'}</h2>
            <p className='lab-blurb'>
                {'A clip shared into a channel is a post Mattermost draws with the plugin’s card, in Mattermost’s own theme. Shared from the viewer or the editor, it lands in the lab’s channel too.'}
            </p>
            <LabSharedClips setLinked={setLabLink}/>
        </div>
    );
};

// What is in Mattermost's side panel, rendered the way sidebar_right.tsx
// renders it: nothing when it is closed or hidden by the Threads page, a thread
// when one is selected, the plugin's panel when it is the one there, and
// otherwise another plugin's or one of Mattermost's own. The panel is unmounted
// whenever anything else is there, exactly as on a server.
const LabSidePanel: React.FC<{panelKey: string; gone: boolean}> = ({panelKey, gone}) => {
    const labState = useLabState();
    const rhs = labState.views.rhs;

    if (!rhs.isSidebarOpen || labState.views.rhsSuppressed) {
        return (
            <div className='lab-rhs-away'>
                {labState.views.rhsSuppressed ?
                    'The Threads page: Mattermost hides the side panel.' :
                    'The side panel is closed. The App Bar icon opens it.'}
            </div>
        );
    }

    if (rhs.selectedPostId !== '' && rhs.rhsState !== 'edit-history') {
        return (
            <LabThread
                key={rhs.selectedPostId}
                root={rhs.selectedPostId}
            />
        );
    }

    if (rhs.rhsState === 'plugin' && rhs.pluggableId === LAB_PANEL) {
        return (
            <>
                {/* Mattermost's own header, as measured from its SCSS:
                    a 56px flex row, the plugin's title node dropped
                    straight into a growing span with 16px of side padding
                    and hidden overflow, then the expand button. (The
                    plugin hides Expand only on a window of about 2230px or
                    more, where it would add nothing, and the close button
                    always.) Reproduced here so the trophy can be seen
                    landing where it is meant to. */}
                <div className='lab-rhs-head'>
                    <span className='lab-rhs-title'><SidebarTitle/></span>
                    <span className='lab-rhs-controls'>
                        {/* A real button, as Mattermost's Expand is: the
                            first thing Mattermost puts focus on when it
                            focuses the side panel (the Lock words row). */}
                        <button
                            type='button'
                            className='lab-rhs-btn'
                            aria-label='Expand'
                            title='Expand'
                            onClick={() => labDispatch({type: 'SET_RHS_EXPANDED', expanded: !rhs.isSidebarExpanded})}
                        >{'⤢'}</button>
                    </span>
                </div>
                <div className='lab-rhs-body'>
                    {gone ? null : <PanelGate key={panelKey}/>}
                </div>
            </>
        );
    }

    const titles: Record<string, string> = {
        search: 'Search results',
        mention: 'Recent mentions',
        flag: 'Saved messages',
        pin: 'Pinned messages',
        'channel-files': 'Channel files',
        'channel-info': 'Info',
        'channel-members': 'Members',
        'edit-history': 'Edit history',
    };
    const title = rhs.rhsState === 'plugin' ?
        (rhs.pluggableId === '' ? '' : 'Another plugin’s panel') :
        titles[rhs.rhsState || ''] || 'Side panel';

    // Search results and their kin: a result opens its thread the way
    // Mattermost's does, remembering the results so its Back arrow can return
    // to them.
    const results = rhs.rhsState === 'search' || rhs.rhsState === 'mention' || rhs.rhsState === 'flag' || rhs.rhsState === 'pin';
    const channelId = labState.entities.channels.currentChannelId;

    return (
        <>
            <LabPanelHead title={title}/>
            <div className='lab-rhs-other'>
                <span className='lab-note'>{title === '' ? 'An empty side panel.' : `Mattermost’s ${title.toLowerCase()} would be here.`}</span>
                {results && rootsIn(channelId).slice(-2).map((post) => (
                    <button
                        key={post.id}
                        type='button'
                        className='lab-result'
                        onClick={() => labDispatch({type: 'SELECT_POST', postId: post.id, channelId, timestamp: Date.now(), previousRhsState: rhs.rhsState})}
                    >{post.message}</button>
                ))}
            </div>
        </>
    );
};

const LabPanelHead: React.FC<{title: string; onBack?: () => void}> = ({title, onBack}) => (
    <div className='lab-rhs-head'>
        {onBack && (
            <button
                type='button'
                className='lab-rhs-btn lab-rhs-close lab-rhs-back'
                aria-label='Back'
                title='Back to the results'
                onClick={onBack}
            >{'‹'}</button>
        )}
        <span className='lab-rhs-title'>{title}</span>
        <span className='lab-rhs-controls'>
            <button
                id='rhsCloseButton'
                type='button'
                className='lab-rhs-btn lab-rhs-close'
                aria-label='Close'
                onClick={labCloseSidePanel}
            >{'✕'}</button>
        </span>
    </div>
);

const NAMES: Record<string, string> = Object.fromEntries(LAB_USERS.map((user) => [user.id, user.first_name || user.username || user.id]));

const clock = (at: number): string => new Date(at).toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'});

// One message, in the thread or in the channel. Focusable, as Mattermost's
// posts are, so focus can be left on one. A shared clip's post is drawn as
// its card, as Mattermost draws a plugin's post type.
const LabMessage: React.FC<{post: LabPost; onRetry?: () => void}> = ({post, onRetry}) => (
    <div
        className='lab-message'
        tabIndex={0}
        data-pending={post.id === post.pending_post_id ? 'true' : undefined}
    >
        <div className='lab-post'>
            <span className='lab-post-name'>{NAMES[post.user_id] || post.user_id}</span>
            <span className='lab-post-time'>{clock(post.create_at)}</span>
        </div>
        {postTypeOf(post) === CLIP_POST_TYPE || postTypeOf(post) === PLAN_POST_TYPE ? (
            // A shared clip's or plan's post: its card, in Mattermost's
            // theme, as the registered post type draws it in a channel or a
            // thread.
            <div className='lab-post-body lab-post-card'>
                <LabCentre>
                    <LabPostBody post={post}/>
                </LabCentre>
            </div>
        ) : (
            <div className='lab-post-body'>{post.message}</div>
        )}
        {post.failed && (
            <div className='lab-note lab-failed'>
                {'Message failed. '}
                <button
                    type='button'
                    className='lab-link'
                    onClick={onRetry}
                >{'Retry'}</button>
            </div>
        )}
    </div>
);

// Mattermost's thread in the side panel: the messages, the reply box, and the
// things that keep the panel from coming back when the wait after a reply is
// over — a file waiting to go, an edit open, the emoji picker, a message's
// "…" menu, or focus left on a message.
const LabThread: React.FC<{root: string}> = ({root}) => {
    const labState = useLabState();
    const rhs = labState.views.rhs;
    const {posts, postsInThread} = labState.entities.posts;
    const first = posts[root];
    const replies = (postsInThread[root] || []).map((id) => posts[id]).filter((post): post is LabPost => Boolean(post));
    const [menu, setMenu] = React.useState(false);
    const [emoji, setEmoji] = React.useState(false);
    const [editing, setEditing] = React.useState(false);
    const [attached, setAttached] = React.useState(false);
    const menuFirst = React.useRef<HTMLButtonElement | null>(null);
    const emojiFirst = React.useRef<HTMLButtonElement | null>(null);

    // Mattermost's menus and its emoji picker take focus as they open.
    React.useEffect(() => {
        if (menu) {
            menuFirst.current?.focus();
        }
    }, [menu]);
    React.useEffect(() => {
        if (emoji) {
            emojiFirst.current?.focus();
        }
    }, [emoji]);

    const back = rhs.previousRhsState && ['search', 'mention', 'flag', 'pin'].includes(rhs.previousRhsState) ?
        () => labDispatch({type: 'RHS_GO_BACK', state: rhs.previousRhsState}) :
        undefined;

    return (
        <>
            <LabPanelHead
                title='Thread'
                onBack={back}
            />
            <div className='lab-rhs-other lab-thread'>
                {first ? <LabMessage post={first}/> : <span className='lab-note'>{'This message has been deleted.'}</span>}
                {replies.map((post) => (
                    <LabMessage
                        key={post.id}
                        post={post}
                        onRetry={() => labRetryReply(post.id)}
                    />
                ))}
                {editing && (
                    <textarea
                        id='edit_textbox'
                        className='lab-reply'
                        aria-label='Edit'
                        defaultValue='An edit in progress'
                    />
                )}
                {attached && <div className='file-preview__container lab-attachment'>{'photo.png'}</div>}
                <textarea
                    id='reply_textbox'
                    className='lab-reply'
                    placeholder='Reply…'
                    aria-label='Reply'
                    onKeyDown={(event) => {
                        if (event.key === 'Enter' && !event.shiftKey) {
                            event.preventDefault();
                            setAttached(false);
                            sendReply(root);
                        }
                    }}
                />
                <div className='lab-thread-tools'>
                    <button
                        type='button'
                        className='lab-chip'
                        onClick={() => {
                            setAttached(false);
                            sendReply(root);
                        }}
                    >{'Send'}</button>
                    <button
                        type='button'
                        className='lab-chip'
                        aria-pressed={attached}
                        onClick={() => setAttached(!attached)}
                    >{'Attach'}</button>
                    <button
                        type='button'
                        className='lab-chip'
                        aria-pressed={editing}
                        onClick={() => setEditing(!editing)}
                    >{'Edit'}</button>
                    <button
                        type='button'
                        className='lab-chip'
                        aria-pressed={emoji}
                        aria-label='Emoji picker'
                        onClick={() => setEmoji(!emoji)}
                    >{'☺'}</button>
                    <button
                        type='button'
                        className='lab-chip'
                        aria-pressed={menu}
                        aria-label='Message actions'
                        onClick={() => setMenu(!menu)}
                    >{'…'}</button>
                    <button
                        type='button'
                        className='lab-chip'
                        title='The thread’s first message deleted: Mattermost leaves the panel open and empty'
                        onClick={() => labDispatch({type: 'POST_REMOVED', data: {id: root}})}
                    >{'Delete root'}</button>
                </div>
                {emoji && (
                    <div
                        role='dialog'
                        aria-label='Emoji picker'
                        className='lab-popup'
                    >
                        {['👍', '🎉', '😂'].map((glyph, index) => (
                            <button
                                key={glyph}
                                ref={index === 0 ? emojiFirst : undefined}
                                type='button'
                                className='lab-chip'
                                onClick={() => setEmoji(false)}
                            >{glyph}</button>
                        ))}
                    </div>
                )}
                {menu && (
                    <div
                        role='menu'
                        className='a11y__popup lab-popup'
                    >
                        <button
                            ref={menuFirst}
                            type='button'
                            role='menuitem'
                            className='lab-chip'
                            onClick={() => setMenu(false)}
                        >{'Mark as unread'}</button>
                        <button
                            type='button'
                            role='menuitem'
                            className='lab-chip'
                            onClick={() => setMenu(false)}
                        >{'Copy link'}</button>
                    </div>
                )}
            </div>
        </>
    );
};

// The channel, between the lab's switches and the side panel: its header, its
// message list and its message box, with the ids Mattermost's have, which is
// what the small player measures itself against. A press anywhere in it
// collapses an expanded side panel, as Mattermost's own does
// (sidebar_right.tsx), which is why a Reply arrow here is the mouse and the
// Reply chip is the keyboard.
const LabCenter: React.FC<{listKey: number}> = ({listKey}) => {
    const labState = useLabState();
    const clipLink = useClipLink();
    const rhs = labState.views.rhs;
    const channelId = labState.entities.channels.currentChannelId;
    const channel = LAB_CHANNELS.find((candidate) => candidate.id === channelId);
    const elsewhere = labState.entities.teams.currentTeamId !== LAB_TEAM.id;
    const roots = rootsIn(channelId);
    const replies = labState.entities.posts.postsInThread;
    const gallery = useGallery();

    return (
        <div
            className='lab-center'
            onMouseDown={() => {
                if (rhs.isSidebarOpen && rhs.isSidebarExpanded) {
                    labDispatch({type: 'SET_RHS_EXPANDED', expanded: false});
                }
            }}
        >
            <div
                id='app-content'
                className='app__content lab-app-content'
            >
                <div
                    id='channel-header'
                    className='lab-channel-header'
                >
                    {elsewhere ? `${LAB_OTHER_TEAM.display_name} › ` : ''}{channelId === '' ? 'Threads (no channel open)' : channel?.display_name || 'A channel'}
                </div>
                {channelId === 'c-arma' && (
                    <div className='lab-bookmarks'>{'Bookmarks · Mod list · Server rules · Slot sheet'}</div>
                )}
                {/* A clip link's page, in Mattermost's centre in place of the
                    channel, until it sends Mattermost on. */}
                {clipLink ? <LabClipLink/> : null}
                <div
                    id='post-list'
                    key={`${channelId}-${listKey}`}
                    className='lab-post-list'
                    hidden={Boolean(clipLink)}
                >
                    <div className='lab-post-scroll'>
                        {/* The 3.2.0 cards in every state, in place of the
                            channel's messages while the Plans row says. */}
                        {gallery === '' ? null : <LabGallery320/>}
                        {gallery !== '' ? null : roots.map((post) => (
                            <div
                                key={post.id}
                                className='lab-center-post'
                            >
                                <div
                                    className='lab-center-click'
                                    onClick={(event) => {
                                        // A button in a post — a shared clip's
                                        // Watch — is its own; Mattermost opens
                                        // the thread on a click anywhere else.
                                        if ((event.target as Element).closest('button, a')) {
                                            return;
                                        }

                                        labDispatch({type: 'SELECT_POST', postId: post.id, channelId, timestamp: Date.now()});
                                    }}
                                >
                                    <LabMessage post={post}/>
                                </div>
                                <span className='lab-note'>{`${(replies[post.id] || []).length} replies`}</span>
                                <button
                                    type='button'
                                    className='lab-reply-arrow'
                                    aria-label='Reply'
                                    title='Reply'
                                    onClick={() => labDispatch({type: 'SELECT_POST', postId: post.id, channelId, timestamp: Date.now(), previousRhsState: rhs.rhsState})}
                                >{'↩'}</button>
                            </div>
                        ))}
                    </div>
                </div>
                <div
                    id='post-create'
                    className='lab-post-create'
                >
                    <textarea
                        id='post_textbox'
                        className='lab-reply'
                        placeholder={`Write to ${channel?.display_name || 'the channel'}`}
                        aria-label='Message'
                        onKeyDown={(event) => {
                            if (event.key !== 'Enter' || event.shiftKey) {
                                return;
                            }

                            // Enter, as Mattermost's box sends it: through
                            // the plugins' MessageWillBePosted hooks first.
                            event.preventDefault();
                            const box = event.currentTarget;
                            const result = messageWillBePosted({message: box.value});
                            if ('error' in result) {
                                labSay(`Mattermost refused to post it: “${result.error.message}”`);

                                return;
                            }

                            labSay(box.value.trim() === '' ? 'Nothing to post.' : 'Posted to the channel (the lab posts nothing anywhere).');
                            box.value = '';
                        }}
                    />
                </div>
            </div>
        </div>
    );
};

// The small player, mounted once at the root of the lab as Mattermost mounts
// a Global component, outside the side panel. With "Delay mini mount by one
// frame" it arrives a frame after the panel goes, as it may on a server whose
// React renders the two at different times.
const LabMiniHost: React.FC = () => {
    const away = useAway();
    const switches = useSwitches();
    const [phase, setPhase] = React.useState(away.phase);
    const [held, setHeld] = React.useState(false);
    if (phase !== away.phase) {
        setPhase(away.phase);
        setHeld(switches.delayMini && away.phase === 'away');
    }

    React.useEffect(() => {
        if (!held) {
            return undefined;
        }

        const timer = window.setTimeout(() => setHeld(false), 17);

        return () => window.clearTimeout(timer);
    }, [held]);

    return held ? null : <MiniPlayerHost/>;
};

// The media counters, sampled every 50 ms: the WHEP exchanges the mock has
// been asked for, and how many <video> elements in the page are in it, not
// muted and not paused, now and at most since the counters were zeroed. More
// than one at any sample is two things audible at once. Paused ones do not
// count: a clip in the Clips dialog sits paused and unmuted while the stream
// it gave the sound back to plays.
let unmutedMost = 0;

const MediaCounters: React.FC = () => {
    const [line, setLine] = React.useState('');

    React.useEffect(() => {
        const sample = () => {
            const unmuted = Array.from(document.querySelectorAll('video')).
                filter((video) => video.isConnected && !video.muted && !video.paused).length;
            unmutedMost = Math.max(unmutedMost, unmuted);

            const counters = labCounters();
            const stop = lastSessionStop();
            const next = [
                `plays ${counters.plays}`,
                `stops ${counters.stops}`,
                `live ${counters.live}`,
                `peeks ${counters.peeks}`,
                `stills ${counters.stills}`,
                `clip requests ${counters.clips} (cards ${counters.cards})`,
                `sessions ${sessionCount()}`,
                `audible videos ${unmuted} now / ${unmutedMost} max`,
                stop ? `last stop: ${stop.handle} (${stop.reason}) at ${new Date(stop.at).toLocaleTimeString()}` : 'no stop yet',
            ].join(' · ');
            setLine((current) => (current === next ? current : next));
        };

        sample();
        const timer = window.setInterval(sample, 50);

        return () => window.clearInterval(timer);
    }, []);

    return (
        <>
            <span className='lab-note lab-counters'>{line}</span>
            <button
                type='button'
                className='lab-chip'
                onClick={() => {
                    resetLabCounters();
                    unmutedMost = 0;
                }}
            >{'zero'}</button>
        </>
    );
};

// The System Console side of the plugin: the whole page, built from
// plugin.json the way Mattermost builds it, with the plugin's own controls in
// their places (consolePage.tsx). Every one of them is rendered: the airtime
// tools were once missing, and a download button that did nothing went
// unnoticed for exactly that reason.
//
// More than the four people the birthday editor shows unfolded, so the page
// opens on the folded list and its summary, which is what a real server with a
// long list sees.
const LAB_BIRTHDAYS = JSON.stringify([
    {username: 'frank', name: 'Frank Hale', birthday: '08-17', birth_year: 1985},
    {username: 'dave', name: 'Dave Jones', birthday: '10-04', birth_year: 1988},
    {username: 'bob', name: 'Bob Turner', birthday: '12-30', birth_year: 1995},
    {username: 'erin', name: 'Erin Cole', birthday: '02-29', birth_year: 2000},
    {username: 'carol', name: 'Carol Fisher', birthday: '03-14', birth_year: 1990},
    {username: 'judy', name: 'Judy Ward', birthday: '11-02', birth_year: 2001},
    {name: 'Ivan Fisher', birthday: '06-09', birth_year: 2018, parent_username: ['carol']},
    {username: '', name: 'Somebody new', birthday: '01-01', birth_year: 1990},
], null, 2);

// The Access card's list states. Chosen here, then seen under Show › System
// Console, where the card sits in the Access section and reads the list once
// as it opens.
function manyLetIn(count: number): LetInPerson[] {
    return Array.from({length: count}, (_, index) => ({
        userId: `u-many-${String(index).padStart(20, '0')}`,
        username: `viewer${String(index + 1).padStart(3, '0')}`,
        name: '',
        at: Date.now() - (index * 3600000),
        byUsername: 'victor',
        state: 'active' as const,
    }));
}

const LET_IN_ERRORS = ['', 'no_user', 'bot', 'deactivated', 'list_full', 'newer', 'busy', 'store', 'bad_request'];

// Lit only for the four sampleLetIn gives, Victor among them: the lab starts
// with the other three, which no chip here makes.
function isSampleLetIn(people: LetInPerson[]): boolean {
    const sample = sampleLetIn().map((person) => person.userId).sort();
    const ids = people.map((person) => person.userId).sort();

    return ids.length === sample.length && ids.every((userId, index) => userId === sample[index]);
}

const AccessStates: React.FC = () => {
    const world = useWorld();

    return (
        <>
            <button
                type='button'
                className='lab-chip'
                aria-pressed={isSampleLetIn(world.letInPeople)}
                title='Caro, Erin (deactivated: no Undo once removed, since the server refuses to let a deactivated account in), a deleted account (no Undo either) and Victor himself, “(you)”. Adding bob shows a name that only repeats the username: just @bob'
                onClick={() => change((current) => {
                    current.letInPeople = sampleLetIn();
                    current.letIn = true;
                })}
            >{'four people'}</button>
            <button
                type='button'
                className='lab-chip'
                aria-pressed={world.letInPeople.length === 0}
                title='Nobody let in: “Nobody is let in. Everybody needs their own bearer token.”'
                onClick={() => change((current) => {
                    current.letInPeople = [];
                    current.letIn = false;
                })}
            >{'empty'}</button>
            <button
                type='button'
                className='lab-chip'
                aria-pressed={world.letInPeople.length >= 500}
                title='500 of 500: the card shows the first eight and Show all 500 (a row taken off, let in again or with focus stays shown); adding somebody new is refused, list_full'
                onClick={() => change((current) => {
                    current.letInPeople = manyLetIn(500);
                })}
            >{'full (500)'}</button>
            <button
                type='button'
                className='lab-chip'
                aria-pressed={world.letInProblem !== ''}
                title='The server set a garbled list aside: its problem at the top of the card'
                onClick={() => change((current) => {
                    current.letInProblem = current.letInProblem === '' ? 'The let-in list could not be read and was set aside. Nobody is let in until people are added again.' : '';
                })}
            >{'problem'}</button>
            <button
                type='button'
                className='lab-chip'
                aria-pressed={world.letInReadOnly}
                title='Saved by a newer Matty: shown, and nothing can be changed'
                onClick={() => change((current) => {
                    current.letInReadOnly = !current.letInReadOnly;
                    current.letInProblem = current.letInReadOnly ? 'The let-in list was saved by a newer Matty. Nobody is let in until that version is back.' : '';
                })}
            >{'read only'}</button>
            {LET_IN_ERRORS.map((code) => (
                <button
                    key={code || 'none'}
                    type='button'
                    className='lab-chip'
                    aria-pressed={world.letInAddError === code}
                    title={code === '' ? 'Adds work: dave (linked, so the note says it only matters if he unlinks), carol (not linked), or a name nobody has (404)' : `The next add is refused with ${code}`}
                    onClick={() => change((current) => {
                        current.letInAddError = code;
                    })}
                >{code === '' ? 'adds work' : `add: ${code}`}</button>
            ))}
            <span className='lab-note'>{'Unlink: victor (linked or not, his own panel follows), dave (linked, let in only if added), carol (never linked: “had no linked stream.”)'}</span>
            <span className='lab-note'>{'the card is under Show › System Console, in Access, and loads when it opens'}</span>
        </>
    );
};

// A made-up proxy password too, so the page shows how Mattermost draws a
// secret: masked.
const CONSOLE_SEED = {birthdays: LAB_BIRTHDAYS, basicauthusername: 'proxy', basicauthpassword: 'lab-proxy-password'};

const Lab: React.FC = () => {
    const [width, setWidth] = React.useState(400);
    const [console_, setConsole] = React.useState(false);
    const [slowTune, setSlowTune] = React.useState(false);
    const [popout, setPopout] = React.useState(false);
    const world = useWorld();

    // A new message list for each channel switch, as Mattermost renders one.
    const [listKey, setListKey] = React.useState(0);
    const labState = useLabState();
    const rhs = labState.views.rhs;
    const switchChannel = React.useCallback(() => {
        const current = labStore.getState() as {entities: {channels: {currentChannelId: string}}; views: {rhs: {isSidebarExpanded: boolean}}};
        const next = current.entities.channels.currentChannelId === 'c-arma' ? 'c-town' : 'c-arma';
        labDispatch({type: 'SELECT_CHANNEL', data: next});

        // Mattermost collapses an expanded side panel when the channel
        // changes (sidebar_right.tsx).
        if (current.views.rhs.isSidebarExpanded) {
            labDispatch({type: 'SET_RHS_EXPANDED', expanded: false});
        }
        setListKey((key) => key + 1);
    }, []);

    // For the screenshots and for anybody driving the lab from the console:
    // the panel's width and the System Console page, by script
    // (window.__villanetLab).
    React.useEffect(() => {
        labSetWidth = setWidth;
        labShowConsole = setConsole;

        return () => {
            labSetWidth = () => undefined;
            labShowConsole = () => undefined;
        };
    }, []);

    // The remount chips: a new key mounts a new panel, and gone keeps it out
    // of the page for a while first.
    const [panelKey, setPanelKey] = React.useState(0);
    const [panelGone, setPanelGone] = React.useState(false);
    React.useEffect(() => {
        labRemount = () => setPanelKey((key) => key + 1);

        return () => {
            labRemount = () => undefined;
        };
    }, []);
    const remount = React.useCallback((gapMs: number) => {
        if (gapMs <= 0) {
            setPanelKey((key) => key + 1);

            return;
        }

        setPanelGone(true);
        window.setTimeout(() => {
            setPanelKey((key) => key + 1);
            setPanelGone(false);
        }, gapMs);
    }, []);

    // The panel fetches "me" and the profile ONCE, on mount — which is right,
    // because in Mattermost they do not change under you. In here they do, so
    // the switches that change them remount the panel rather than being inert.
    // Keyed on the values themselves, so nothing else causes a remount — except
    // the link, which the panel changes itself from its footer and keeps track
    // of without a refetch, so only the lab's switch for it counts.
    const identity = [
        world.me.userId,
        String(world.linkSwitches),
        String(world.me.isAdmin),
        JSON.stringify(world.me.media || null),
        String(world.me.bannerPlacement),
        world.me.bannerText,
        world.me.bannerColor,
        world.profile.motd,
        String(world.profile.isPublic),
        String(world.profile.available),
    ].join('|');

    return (
        <div
            className='lab'
            data-slow-unlock={useSwitches().slowUnlock ? 'true' : undefined}
        >
            <Controls
                width={width}
                onWidth={setWidth}
                showingConsole={console_}
                onConsole={setConsole}
                tuning={slowTune}
                onTuning={setSlowTune}
                popout={popout}
                onPopout={setPopout}
                onRemount={remount}
                onChannelSwitch={switchChannel}
            />

            {/* Mattermost's popped-out side panel: a window of its own, with
                #root a grid at least as tall as the window, and the panel in
                the flow of the page under its header (components/rhs_popout
                and sass/base/_structure.scss). The two green strips are not
                Mattermost's: they stand for anything else on the page above
                and below the panel, which the panel's height has to allow
                for. */}
            {popout && !console_ ? (
                <div className='lab-popout'>
                    <div id='root'>
                        <div className='lab-popout-banner'>{'CA//INTERNAL'}</div>
                        <div className='main-wrapper rhs-popout'>
                            <div className='sidebar--right'>
                                <div className='sidebar-right__body'>
                                    <div className='lab-rhs-head'>
                                        <span className='lab-rhs-title'><SidebarTitle/></span>
                                    </div>
                                    <PanelGate key={identity}/>
                                </div>
                            </div>
                        </div>
                        <div className='lab-popout-banner'>{'CA//INTERNAL'}</div>
                    </div>
                    <button
                        type='button'
                        className='lab-chip lab-popout-exit'
                        onClick={() => setPopout(false)}
                    >{'Back to the lab'}</button>
                </div>
            ) : null}

            {console_ || popout ? (console_ ? (
                <ConsolePage
                    seed={CONSOLE_SEED}
                    onExit={() => setConsole(false)}
                />
            ) : null) : (
            <div className='lab-stage'>
                <LabCenter listKey={listKey}/>

                {/* Expanded, the side panel lies over the channel and a spacer
                    keeps the channel the width it was, as in Mattermost. */}
                {rhs.isSidebarOpen && rhs.isSidebarExpanded && (
                    <div
                        className='lab-rhs-spacer'
                        style={{width: `${width}px`}}
                    />
                )}
                <div
                    id='sidebar-right'
                    className='lab-rhs'
                    data-expanded={rhs.isSidebarOpen && rhs.isSidebarExpanded ? 'true' : undefined}
                    style={rhs.isSidebarOpen && rhs.isSidebarExpanded ? undefined : {width: `${width}px`}}
                >
                    <LabSidePanel
                        panelKey={`${identity}|${panelKey}`}
                        gone={panelGone}
                    />
                </div>
            </div>
            )}

            <LabMiniHost/>

            {/* The Clips dialog's host, at the root as Mattermost mounts a
                Global component. Not while the Console is shown: the System
                Console renders neither Global nor Root components. */}
            {console_ ? null : <ClipsRoot/>}
        </div>
    );
};

// Mattermost wraps every plugin component in an error boundary of its own
// (PluggableErrorBoundary), so on a server a throw anywhere in the panel
// replaces it with "An error occurred in the Matty plugin." (the manifest's
// name). This is the
// lab's stand-in, with the error itself on show: without it React unmounts
// everything and leaves an empty page that says nothing about why.
//
// The lab runs Mattermost's own React, 18.2.0 (build/check-react.py), so an
// API that React lacks — React 19's use or useActionState, say — is undefined
// in it just as it is on the server.
class Crashed extends React.Component<{children: React.ReactNode}, {error: Error | null}> {
    state = {error: null as Error | null};

    static getDerivedStateFromError(error: Error) {
        return {error};
    }

    render() {
        if (this.state.error) {
            return (
                <div className='lab-crash'>
                    <p>{'An error occurred in the Matty plugin.'}</p>
                    <pre>{String(this.state.error)}</pre>
                </div>
            );
        }

        return this.props.children;
    }
}

injectStyles();
connectLabMattermost();

// Mattermost's emoji search for the soundboard's chooser, and Victor's recent
// emoji as Mattermost keeps them (the preference recent_emojis, named for
// him), two of them the server's own. And Mattermost's mention component for
// @names in titles.
installLabEmoticon();
labDispatch({type: 'RECEIVED_PREFERENCES', data: [{category: 'recent_emojis', name: ME_ID, value: JSON.stringify([
    {name: 'fanfare', usageCount: 9},
    {name: 'fire', usageCount: 7},
    {name: 'joy', usageCount: 6},
    {name: 'skull', usageCount: 4},
    {name: 'wakeup', usageCount: 3},
    {name: '+1', usageCount: 3},
    {name: 'goat', usageCount: 2},
])}]});
installLabPostUtils();

// What the plugin's initialize() starts outside React, in the same order: the
// session pool and its reconciler before anything renders, and the side-panel
// controller on the lab's stand-in for Mattermost's store, with a small player
// to show (LabMiniHost, mounted at the root as Mattermost mounts a global
// component). The lab's side panel hears about every change of that store
// before the controller does, as Mattermost's own components do on the legacy
// root this lab renders with by default. The profile popover's Watch opens the
// panel, as the registration's showRHSPlugin does. And the access follower
// (access.ts), before anything renders, as initialize() installs it: the lab's
// buttons call the same handlers its registry would (the "me" event, the
// reconnect, a message being posted), so its registry registers nothing.
const LAB_ACCESS_REGISTRY = {
    registerWebSocketEventHandler: () => undefined,
    unregisterWebSocketEventHandler: () => undefined,
    registerReconnectHandler: () => undefined,
    unregisterReconnectHandler: () => undefined,
    registerMessageWillBePostedHook: () => undefined,
};
installSessions();
installReconciler(() => getAway().phase !== 'idle');
installTwitch();
installAway({store: labStore, sidebarId: LAB_PANEL, show: labShowPanel, miniHost: true});
setSidebarOpener(labShowPanel);
installAccess(LAB_ACCESS_REGISTRY, ME_EVENT, meHeard);
followAccountsAccess();
installBoards();

// The App Bar icon, as index.tsx wraps the registration's toggle: a press
// that opens the panel is the person's own opening (the lock may focus its
// bar) and a plain one (anything waiting for the lock is let go).
const toggleByButton = openerPress(labTogglePanel);

// Mattermost focusing the side panel as it opens (sidebar_right.tsx
// handleRHSFocus, from 10.11): a timer hop, focusElement's second hop, then
// the first focusable thing in the side panel, which is the header's Expand
// button. Only when the side panel goes from closed to open: its list of
// content changes that also move focus (pinned, mentions, saved, files, info,
// members, edit history, another channel or team) has no plugin view in it,
// so the panel taking the place of an open thread or search moves nothing, and
// focus stays on the icon that was pressed. On the Lock words row; off,
// nothing moves focus but the plugin.
type LabRhs = {isSidebarOpen: boolean; rhsState: string | null; pluggableId: string};
const labRhsNow = (): LabRhs => (labStore.getState() as {views: {rhs: LabRhs}}).views.rhs;
let sidePanelWasOpen = labRhsNow().isSidebarOpen;
labStore.subscribe(() => {
    const rhs = labRhsNow();
    const shown = rhs.isSidebarOpen && rhs.rhsState === 'plugin' && rhs.pluggableId === LAB_PANEL;
    const opened = shown && !sidePanelWasOpen;
    sidePanelWasOpen = rhs.isSidebarOpen;
    if (!opened || !labSwitches.rhsFocus) {
        return;
    }

    window.setTimeout(() => window.setTimeout(() => {
        const first = document.querySelector<HTMLElement>('#sidebar-right button:not([disabled]), #sidebar-right [href], #sidebar-right input:not([disabled]), #sidebar-right [tabindex="0"]');
        first?.focus();
        labSay(first ? 'Mattermost put focus on the side panel’s first button, two timer hops after it opened.' : 'Mattermost found nothing to focus in the side panel.');
    }, 0), 0);
});

// The lab's stand-in for Mattermost's store, on the console too, so a side
// panel the chips do not offer (pinned, saved, files, info, members, edit
// history) can be dispatched by hand: __villanetLabStore.dispatch({type:
// 'UPDATE_RHS_STATE', state: 'pin'}).
(window as unknown as {__villanetLabStore?: unknown}).__villanetLabStore = labStore;

// The panel polls on its own, but the control panel changes the world between
// polls and nobody wants to wait thirty seconds to see what they just clicked.
// That stands for the server's pushes, which "Freeze pushes" stops.
subscribe(() => {
    if (!labSwitches.freezePushes) {
        setSnapshot(snapshotOf());

        // The page reads the boards a moment after a snapshot says they have
        // changed, so that not every window asks at once. Here, at once:
        // whoever changed the world is looking at what that did.
        void loadBoards();
    }
});
setSnapshot(snapshotOf());

// Whether the lab is holding "me" back as not known (a fresh page with no
// answer, or nobody signed in), and the panel's remount, which a fresh page
// needs so the panel reads "me" again as it mounts.
let identityUnknown = false;
let labRemount: () => void = () => undefined;

// The Lab's own width and console switches, for window.__villanetLab.
let labSetWidth: (px: number) => void = () => undefined;
let labShowConsole: (on: boolean) => void = () => undefined;

// The lab, by script, for screenshots: the panel's width, the System Console
// page, the world and its Twitch scenarios, and watching a stream as a card
// does. Lab only; nothing like it exists in the plugin.
(window as unknown as {__villanetLab?: unknown}).__villanetLab = {
    width: (px: number) => labSetWidth(px),
    console: (on: boolean) => labShowConsole(on),
    change,
    getWorld,
    snapshotOf,
    setVictorTwitch,
    toggleTwitchLive,
    victorSimulcast,
    toggleErinBroadcastBox,
    pauseErinOnePoll,
    setTwitchLedger,
    freshPage: (mode: 'linked' | 'letIn' | 'locked') => freshPage(mode),
    showPanel: labShowPanel,
    watch: (handle: string) => {
        const stream = snapshotOf().streams.find((item) => item.handle === handle);
        if (stream) {
            watchOrOpen(stream, 'only');
        }
    },
    add: (handle: string) => {
        const stream = snapshotOf().streams.find((item) => item.handle === handle);
        if (stream) {
            watchOrOpen(stream, 'toggle');
        }
    },
    handleOf: (userId: string) => snapshotOf().streams.find((item) => item.userId === userId && !item.source)?.handle || '',

    // 3.2.0's states (lab/rows320.tsx).
    v320: labHooks320,

    // 3.4.0's states: Steam and Hytale (lab/rows340.tsx), and the Playing
    // line's check, which resolves to {ok, steps}.
    v340: {...labHooks340, playingStaysAtStart: () => playingStaysAtStart((px) => labSetWidth(px))},

    // The clip player (lab/clipPlayer.ts).
    clipPlayer: labClipPlayer,
};

// Who is reading, as the plugin's initialize() has normally fetched it long
// before anybody opens the panel (src/index.tsx), which the panel goes by.
// Taken again whenever the lab changes "me" as a reload would; not while the
// lab's server has changed underneath a page that has not been told yet
// (labSwitches.held: Let out now and the rest), which only reading "me"
// again — through a refusal, the "me" event, a reconnect, the clock, a
// window looked at — may bring in. Keyed, so a change the page already knows
// does not set it again.
let knownAs = '';
const knowMe = () => {
    if (identityUnknown || labSwitches.held) {
        return;
    }

    const {me, linkSwitches} = getWorld();
    const fields = meOf();
    const key = [
        me.userId,
        String(linkSwitches),
        String(me.isAdmin),
        JSON.stringify(fields.media || null),
        fields.access || '',
        String(fields.popOut || false),
        String(fields.alsoLetIn || false),
        String(fields.configured),
        fields.letInAlert || '',
        fields.clipsAlert || '',
        JSON.stringify(fields.clipsStreamer || null),

        // Twitch: connecting, disconnecting, a takeover notice, a block and
        // the switch all come to the panel through "me", which it follows
        // without remounting — so the Connect sheet is still there to say
        // Connected when the window has done its work.
        JSON.stringify(fields.twitch || null),

        // Steam and Hytale (design/3.4.0.md 2.9), the same way.
        JSON.stringify(fields.steam || null),
        JSON.stringify(fields.hytale || null),
    ].join('|');
    if (key !== knownAs) {
        knownAs = key;

        // The name and the artwork come with "me" in the same tick, as
        // identity.ts refreshIdentity takes them, so the title never
        // disagrees with the panel after a reload.
        takeBrand(fields);
        setIdentity(meOf());
    }
};
subscribe(knowMe);

// A reload that the Server name row asked for (labReloadMeLate): the lab's
// server keeps the name, the artwork and the banner it had, and "me" is three
// seconds late, so what this browser remembered of them (name.ts, banner.ts)
// is what the page draws until the answer comes. Otherwise "me" is known at
// once, as initialize() has normally read it long before the panel opens.
const RELOAD_KEY = 'lab.reloadMeLate';
const reloaded = (() => {
    try {
        const text = window.sessionStorage.getItem(RELOAD_KEY);
        window.sessionStorage.removeItem(RELOAD_KEY);

        return text ? JSON.parse(text) as Partial<ReturnType<typeof getWorld>['me']> : null;
    } catch {
        return null;
    }
})();
if (reloaded) {
    identityUnknown = true;
    change((current) => {
        current.me = {...current.me, ...reloaded};
        current.meDelayMs = 3000;
    });
    identityUnknown = false;
} else {
    knowMe();
}

function labReloadMeLate(): void {
    const {name, artwork, bannerText, bannerColor, bannerPlacement} = getWorld().me;
    try {
        window.sessionStorage.setItem(RELOAD_KEY, JSON.stringify({name, artwork, bannerText, bannerColor, bannerPlacement}));
    } catch {
        // No session storage: a plain reload, with "me" at once.
    }
    window.location.reload();
}

// The Server name row's names: the default, another, one of several words,
// and one of the longest the setting allows (32 characters).
const LAB_NAMES = ['Matty', 'VillaNET', 'Movie Night Club', 'Saturday Night Film & Games Club'];

// An admin changing the server's name or artwork: the server tells every page
// with the "me" event, which carries both (src/me.ts).
function labServerBrand(next: {name?: string; artwork?: {emblem: boolean; ghost: boolean}}): void {
    change((current) => {
        current.me = {...current.me, ...next};
    });
    const {name, artwork} = getWorld().me;
    meHeard({data: {name, artwork}});
}

// "me" read again, by whatever read it: once the page has the server's word
// for Victor's access, the lab follows the world again.
onIdentityChange(() => {
    if (labSwitches.held && getIdentity() !== null && latestAccess() === labAccessOf(getWorld())) {
        setSwitch('held', false);
        knownAs = '';
        knowMe();
    }
});

// The server changes under a page that is not told: "me" here goes on saying
// what it said until something reads it again.
function changeUnseen(apply: (current: ReturnType<typeof getWorld>) => void): void {
    setSwitch('held', true);
    change(apply);
}

// The Access row: a fresh page that knows Victor as the world has him, as
// after a reload. Nothing carries over — no tiles, nothing waiting, no reason
// for a lock — and the panel mounts again and reads "me", which unknown holds
// back altogether and starting holds back for two seconds.
function freshPage(mode: 'linked' | 'letIn' | 'locked' | 'unknown' | 'starting'): void {
    setSwitch('held', false);
    signedOut();
    resetClips();
    forgetTagSearch();
    clearAccounts();
    change((current) => {
        const key = mode === 'linked' ? 'live-victor' : '';
        if (key !== current.me.streamKey) {
            current.victorSource = key === '' ? 'none' : 'manual';
            current.streams = current.streams.map((item) => (
                item.userId === ME_ID ? {...item, linkSource: key === '' ? undefined : 'manual'} : item
            ));
        }
        current.me = {...current.me, streamKey: key};
        current.letIn = mode === 'letIn';
        current.letInPeople = current.letInPeople.filter((person) => person.userId !== ME_ID).concat(
            mode === 'letIn' ? [{userId: ME_ID, username: 'victor', name: 'Victor', at: Date.now(), byUsername: 'victor', state: 'active'}] : [],
        );
        current.meWithheld = mode === 'unknown';
        current.meDelayMs = mode === 'starting' ? 2000 : 0;
        current.storeBlips = 0;
    });
    identityUnknown = mode === 'unknown' || mode === 'starting';
    setIdentity(null);
    knownAs = '';
    if (!identityUnknown) {
        knowMe();
    } else {
        identityUnknown = false;
    }
    labRemount();
}

// Let in → let out, while watching: the server refuses from now on, and the
// next request a tile makes carries the refusal to the page — unless the
// socket is down, when nothing does until Reconnect or Clock +60 s.
function letOutNow(): void {
    if (labAccessOf(getWorld()) !== 'letIn') {
        labSay('Victor is not let in: pick Access: let in first, and watch somebody.');

        return;
    }

    changeUnseen((current) => {
        current.letIn = false;
        current.letInPeople = current.letInPeople.filter((person) => person.userId !== ME_ID);
    });
    if (labSwitches.socketDown) {
        labSay('Let out with the socket down: the page has not heard. Reconnect, or Clock +60 s while something plays.');

        return;
    }

    labRefusal('locked');
    labSay('Let out: a tile’s next request was refused with locked, and the page read “me” again.');
}

// Locked → let in, while the lock is showing: the "me" event arrives.
function letInNow(): void {
    if (labAccessOf(getWorld()) !== 'locked') {
        labSay('Victor is not locked: pick Access: locked first.');

        return;
    }

    changeUnseen((current) => {
        current.letIn = true;
        current.letInPeople = current.letInPeople.filter((person) => person.userId !== ME_ID).concat(
            [{userId: ME_ID, username: 'victor', name: 'Victor', at: Date.now(), byUsername: 'victor', state: 'active'}],
        );
    });
    if (labSwitches.socketDown) {
        labSay('Let in with the socket down: the "me" event was dropped. Reconnect, or Window focused.');

        return;
    }

    meHeard();
    labSay('An admin let Victor in: the “me” event arrived, and the page read “me” again.');
}

// The set-up for Unlink while let in: linked and on the list, Dave playing,
// and the editor open on a draft of his stream.
function linkedLetInEditing(): void {
    freshPage('linked');
    change((current) => {
        current.letIn = true;
        current.letInPeople = current.letInPeople.filter((person) => person.userId !== ME_ID).concat(
            [{userId: ME_ID, username: 'victor', name: 'Victor', at: Date.now(), byUsername: 'victor', state: 'active'}],
        );
        current.captureMode = 'ok';
    });
    labShowPanel();
    window.setTimeout(() => {
        const handle = watchDave();
        if (handle) {
            window.setTimeout(() => startClip({handle, title: 'Dave'}, 'live', getWorld().clipLength, null), 600);
        }
    }, 300);
}

// Linked → let in: the link undone elsewhere, and the "me" event arrives.
function unlinkWhileLetIn(): void {
    const world = getWorld();
    if (labAccessOf(world) !== 'linked' || !world.letIn) {
        labSay('Press “linked + let in, editing” first.');

        return;
    }

    changeUnseen((current) => {
        current.me = {...current.me, streamKey: ''};
        current.victorSource = 'none';
    });
    meHeard();
    labSay('Victor’s link was undone elsewhere; he is still let in. The editor closes, drafts go, the panel remounts watch-only, and the streams play on.');
}

// Unlocked in another window: the server says linked, and this window hears
// nothing until it is looked at again.
function unlockedElsewhere(): void {
    changeUnseen((current) => {
        current.me = {...current.me, streamKey: 'live-victor'};
        current.victorSource = 'manual';
    });
    labSay('Unlocked in another window: this one is told nothing. Press Window focused.');
}

function windowFocused(): void {
    window.dispatchEvent(new Event('focus'));
    document.dispatchEvent(new Event('visibilitychange'));
}

// The next "me" says the store did not answer, and something asks for it.
function storeBlip(): void {
    change((current) => {
        current.storeBlips = 1;
    });
    void refreshIdentity();
    labSay('The store did not answer one read of “me”: unknown. Nothing is torn down; it is read again 5 s later.');
}

// For the shared clips' block: a clip link
// opened as somebody linked, or locked.
function setLabLink(mode: 'yes' | 'no'): void {
    freshPage(mode === 'yes' ? 'linked' : 'locked');
}

// The Streamer row: Victor's link and his stream's say over clips, put into
// "me" as a reload would, so the open panel follows without remounting.
function setStreamer(mode: LabStreamer): void {
    setSwitch('held', false);
    if (getWorld().meWithheld) {
        change((current) => {
            current.meWithheld = false;
        });
    }
    identityUnknown = false;
    setLabStreamer(mode);
    knownAs = '';
    knowMe();
}

// "Signed in after load": the page loaded on Mattermost's sign-in page, where
// plugins start with nobody signed in and "me" refused, and Victor signs in a
// moment later without the page loading again. "Signed out, back locked":
// Victor signs out in place, as Mattermost does, and somebody locked signs
// in. Neither touches the identity itself: only Mattermost's store changes,
// and the plugin's own watch on it (identity.ts followSignIn, installed below
// as index.tsx installs it) has to notice both the sign-out and the sign-in.
let signingInAs: 'later' | 'away' = 'later';

function signInAfterLoad(mode: 'later' | 'away'): void {
    if (labSwitches.signingIn) {
        return;
    }

    signingInAs = mode;
    setSwitch('held', false);
    if (getWorld().meWithheld) {
        change((current) => {
            current.meWithheld = false;
        });
    }
    if (mode === 'away') {
        resetLabCounters();
    }
    labDispatch({type: 'LOGOUT_SUCCESS'});
    setSwitch('signingIn', true);
    if (mode === 'away') {
        // Whoever signs in next has neither a stream nor a place on the
        // let-in list. Changed while nobody is signed in, so it is that
        // person's, not Victor's.
        change((current) => {
            current.me = {...current.me, streamKey: ''};
            current.letIn = false;
        });
        labSay('Victor signed out without the page loading again, and somebody locked signs in in three seconds. Every stream stopped; watch the counters: no clip request at all.');
    } else {
        labSay('Nobody is signed in, as on the sign-in page: no clapper and nothing asked for. Victor signs in in three seconds, without the page loading again.');
    }
    window.setTimeout(() => labDispatch({type: 'RECEIVED_ME', data: {id: ME_ID}}), 3000);
}

followSignIn(labStore, () => {
    identityUnknown = false;
    knownAs = '';
    setSnapshot(snapshotOf());
    refreshIdentity().then((me) => {
        setSwitch('signingIn', false);
        const asked = labCounters().clips;
        if (signingInAs === 'away') {
            labSay(me ? `Somebody locked signed in: “me” says so, the dialog stayed closed and ${asked === 0 ? 'no clip request was made' : `${asked} clip requests were made — there should be none`}.` : 'Somebody signed in, and “me” could not be read.');
        } else {
            labSay(me ? 'Victor signed in: “me” and the snapshot were asked for again because the store said so, and the panel was not reopened for it.' : 'Victor signed in, and “me” could not be read.');
        }
    });
}, () => {
    // As index.tsx does on signing out: every stream stopped, the watch list
    // and anything waiting forgotten, the dialog closed, everything fetched
    // for the person dropped, what a hashtag search was told in their name
    // forgotten, and "me" unknown until the next one.
    identityUnknown = true;
    signedOut();
    resetClips();
    forgetTagSearch();
    clearAccounts();
    setIdentity(null);
});

const mount = document.getElementById('lab');
if (mount) {
    if (CONCURRENT_ROOT) {
        createRoot(mount).render(<Crashed><Lab/></Crashed>);
    } else {
        render(<Crashed><Lab/></Crashed>, mount);
    }
}
