import React from 'react';

import {CLIP_POST_TYPE} from '../src/clipLogic';
import {advanceClock, holdClock, now as clockNow} from '../src/clock';
import {Avatar} from '../src/components/Avatar';
import {PostAwardBadges} from '../src/components/AwardBadges';
import {PlanCardFace} from '../src/components/PlanPostCard';
import {PLAN_POST_TYPE, planCardView} from '../src/planLogic';
import {heardSound} from '../src/soundboard';
import {nameNow} from '../src/name';
import {setSnapshot} from '../src/store';
import {viewerZone} from '../src/time';
import type {SoundWhy, VoicePerson} from '../src/types';

import {LabProfileCard, displayNameOf, mentionSettings, setMentionSettings} from './atMention';
import type {MentionMode, NameDisplay} from './atMention';
import {useLabState} from './mattermost';
import type {LabPost} from './mattermost';
import {setLabMicrophone} from './mocks/soundEncode';
import {LabCentre, LabPostBody, THEMES, postTypeOf, setCardLayout, useCardLayout} from './sharedClips';
import type {CentreTheme} from './sharedClips';
import {
    CARD_PLANS,
    HOUR_MS,
    LAB_USERS,
    ME_ID,
    NAMES_TITLE,
    NO_NAME_TITLES,
    TRAILING_TITLES,
    TWITCH_HANDLE_TITLE,
    UNKNOWN_TITLE,
    boardsOf,
    cardPlans,
    cardStartOf,
    change,
    getWorld,
    heldByMe,
    labPlanShareMessage,
    labPlanShareProps,
    labShareMessage,
    labShareProps,
    liveWith,
    sampleSounds,
    snapshotOf,
    startingAchievementValues,
    startingFanHours,
    subscribe,
} from './world';
import type {World} from './world';

// The lab's 3.2.0 rows (design/3.2.0.md 11.3): awards by your name, biggest
// fans, achievements, the soundboard, shared plans, @names in titles and the
// thumb on the clip card. Each row sets the fake server up for a state and,
// where a state is something somebody does (a sound playing, a step of
// adding one, the Trophies card on a tab), drives the real panel to it by
// pressing its own buttons, as a person would. The same on
// window.__villanetLab.v320, for the screenshots.

const pause = (ms: number) => new Promise((resolve) => window.setTimeout(resolve, ms));

function useLabWorld(): World {
    const [, bump] = React.useState(0);
    React.useEffect(() => subscribe(() => bump((value) => value + 1)), []);

    return getWorld();
}

// Outside React, so the rows, the stage and the scripts agree.
function useBump(listeners: Set<() => void>): void {
    const [, bump] = React.useState(0);
    React.useEffect(() => {
        const listener = () => bump((value) => value + 1);
        listeners.add(listener);

        return () => {
            listeners.delete(listener);
        };
    }, [listeners]);
}

const Chip: React.FC<{pressed?: boolean; title?: string; onClick: () => void; children: React.ReactNode}> = ({pressed, title, onClick, children}) => (
    <button
        type='button'
        className='lab-chip'
        aria-pressed={pressed}
        title={title}
        onClick={onClick}
    >{children}</button>
);

type RowProps = {label: string; children: React.ReactNode};

// ---- Awards by your name (3.9) ----

type ChoiceSet = 'auto' | 'everybody' | 'four';

// 3.9's choices: Dave chose this month and a fan title (he is Bob's biggest
// fan), Bob chose nothing at all, Victor chose the binoculars (Erin's now),
// the crown and Podium, so one is lost and skipped, and Erin, a past
// champion holding nothing now, chose her past months. "Victor's four" is
// the most anybody can choose, an achievement and a fan title among them.
function chooseSet(set: ChoiceSet): void {
    change((current) => {
        const at = Date.now() - (3 * 24 * HOUR_MS);
        if (set === 'everybody') {
            current.awardChoices = {
                [ME_ID]: {show: ['snooper', 'alltime', 'ach:podium'], at},
                'u-dave': {show: ['month', 'fan:u-bob'], at},
                'u-bob': {show: [], at},
                'u-erin': {show: ['past'], at},
            };
        } else if (set === 'four') {
            current.awardChoices = {[ME_ID]: {show: ['ach:streamed', 'alltime', 'past', 'fan:u-erin'], at}};
        } else {
            current.awardChoices = {};
        }
        current.awardsReadable = true;
    });
}

function choiceSetOf(world: World): ChoiceSet {
    if (Object.keys(world.awardChoices).length > 1) {
        return 'everybody';
    }

    return world.awardChoices[ME_ID] ? 'four' : 'auto';
}

// NEW in the picker: this browser saw everything Victor holds except his
// first Lo-fi radio hour, which came since.
function seedNewAward(): void {
    const seen = heldByMe().map((award) => award.id).filter((id) => id !== 'ach:ch-1a2b3c4d5e6f');
    try {
        window.localStorage.setItem(`villanet.awardsSeen.${ME_ID}`, JSON.stringify(seen));
    } catch {
        // No NEW marks, which is what the picker does without storage too.
    }
}

// A new step for Victor, the way it arrives: this browser has said every step
// he holds so far, then his figure passes the next one.
function earnStep(family: string, value: number): void {
    const said: Record<string, number> = {};
    const boards = boardsOf();
    const mine = (boards.achievements || []).find((holder) => holder.userId === ME_ID);
    (boards.achievementKinds || []).forEach((kind) => {
        said[kind.id] = mine?.have.find((entry) => entry.id === kind.id)?.tier || 0;
    });
    try {
        window.localStorage.setItem(`villanet.achSeen.${ME_ID}`, JSON.stringify(said));
    } catch {
        // No toast without storage, by design.
    }
    change((current) => {
        current.achievementValues = {...current.achievementValues, [ME_ID]: {...current.achievementValues[ME_ID], [family]: value}};
    });
}

// The Trophies card, opened on one tab, as the trophy opens it.
async function openTrophies(tab: string): Promise<boolean> {
    const open = document.querySelector<HTMLButtonElement>('.vntv-modal-card[aria-label="Trophies"] .vntv-modal-close');
    if (open) {
        open.click();
        await pause(250);
    }
    try {
        window.localStorage.setItem('villanet.trophiesTab', tab);
    } catch {
        // It opens on its first tab.
    }
    const trophy = document.querySelector<HTMLButtonElement>('button.vntv-trophy[aria-label="Trophies"]');
    trophy?.click();
    await pause(350);

    return Boolean(trophy);
}

// ---- Biggest fans (4.9) ----

function setLedgerReset(which: 'fansResetAt' | 'channelsResetAt', on: boolean): void {
    change((current) => {
        const {[which]: _was, ...rest} = current.ledgerAlert;
        current.ledgerAlert = on ? {...rest, [which]: Date.now() - (26 * HOUR_MS)} : rest;
    });
}

// ---- The soundboard (7.12 to 7.14) ----

const SHEET = '.vntv-sheet-sounds';

function sheetOpen(): boolean {
    return document.querySelector(SHEET) !== null;
}

async function openSounds(): Promise<boolean> {
    if (!sheetOpen()) {
        document.querySelector<HTMLButtonElement>('button.vntv-bar-sounds')?.click();
        await pause(450);
    }

    return sheetOpen();
}

async function closeSounds(): Promise<void> {
    if (sheetOpen()) {
        document.querySelector<HTMLButtonElement>(`${SHEET} .vntv-sheet-close`)?.click();
        await pause(450);
    }
}

// The world each state starts from: Victor, Dave and Bob in Lounge, Victor
// talking, the board of nine, nothing playing, nobody waiting, the clock
// running.
const IN_VOICE: VoicePerson[] = [
    {name: 'Victor', userId: ME_ID},
    {name: 'Dave', userId: 'u-dave'},
    {name: 'Bob', userId: 'u-bob', muted: true},
];

function calmSounds(): void {
    holdClock(false);
    setLabMicrophone('allowed');
    change((current) => {
        current.soundboardOn = true;
        current.voiceConnected = true;
        current.voice = IN_VOICE.slice();
        current.soundWhy = '';
        current.soundBotOk = true;
        current.soundCooldownUntil = 0;
        current.soundBusyUntil = 0;
        current.soundEncoder = 'constant';
        current.soundAddAnswer = '';
        current.soundBlocked = current.soundBlocked.filter((person) => person.userId !== ME_ID);
        if (current.sounds.length === 0) {
            current.sounds = sampleSounds();
        }
        current.soundV += 1;
    });
    heardSound({busyUntil: 0}, clockNow());
}

async function press(name: string): Promise<void> {
    document.querySelector<HTMLButtonElement>(`${SHEET} .vntv-snd-tile[aria-label="Play ${name}"]`)?.click();
    await pause(Math.max(400, getWorld().latencyMs + 250));
}

export type SoundState = 'idle' | 'playing' | 'cooldown' | 'busy' | 'muted' | 'deafened' | 'suppressed' | 'not_registered' |
    'two_sessions' | 'left' | 'offline' | 'cannot' | 'empty' | 'yours_full' | 'board_full';

export const SOUND_STATES: SoundState[] = ['idle', 'playing', 'cooldown', 'busy', 'muted', 'deafened', 'suppressed', 'not_registered',
    'two_sessions', 'left', 'offline', 'cannot', 'empty', 'yours_full', 'board_full'];

// A board of 120 for "board full", and Victor with sixteen for "yours full".
function filledBoard(total: number, mine: number): World['sounds'] {
    const base = sampleSounds();
    const people = ['u-dave', 'u-bob', 'u-erin'];

    return Array.from({length: total}, (_, index) => {
        const like = base[index % base.length];
        const ownerId = index < mine ? ME_ID : people[index % people.length];

        return {...like, id: `${index.toString(16).padStart(4, '0')}a1b2c3d4e5f6`, ownerId, name: `${like.name.slice(0, 13)} ${index + 1}`.trim()};
    });
}

// Each state with the sheet open on it. The rings and sweeps are timed by the
// lab's clock, held still once the state is reached, so a picture catches
// them where they were put: the airhorn 40% played, seven seconds of the wait
// to go, somebody else's sound 60% through.
async function soundState(which: SoundState): Promise<string> {
    await closeSounds();
    calmSounds();
    await pause(250);
    if (!(await openSounds())) {
        return 'no soundboard tool: is Victor linked and in voice?';
    }
    await pause(350);

    const why = (reason: SoundWhy) => change((current) => {
        current.soundWhy = reason;
        current.soundV += 1;
    });

    switch (which) {
    case 'playing':
    case 'cooldown':
        holdClock(true);
        await press('Airhorn');
        advanceClock(which === 'playing' ? 960 : 2400 + 3000);
        break;
    case 'busy': {
        holdClock(true);
        const until = clockNow() + 2600;
        change((current) => {
            current.soundBusyUntil = until;
        });
        heardSound({busyUntil: until}, clockNow());
        await pause(300);
        advanceClock(1560);
        break;
    }
    case 'muted':
        change((current) => {
            current.voice = current.voice.map((person) => (person.userId === ME_ID ? {...person, muted: true} : person));
        });
        break;
    case 'deafened':
    case 'suppressed':
    case 'not_registered':
        change((current) => {
            current.voice = current.voice.map((person) => (person.userId === ME_ID ? {...person, muted: true} : person));
        });
        why(which);
        break;
    case 'two_sessions':
        change((current) => {
            current.voice = current.voice.concat({name: 'Victor (phone)', userId: ME_ID});
        });
        break;
    case 'left':
        // Gone from Mumble with the sheet open: the tool goes with the next
        // snapshot, and the sheet says why until it is closed.
        change((current) => {
            current.voice = current.voice.filter((person) => person.userId !== ME_ID);
            current.soundV += 1;
        });
        break;
    case 'offline':
        change((current) => {
            current.voiceConnected = false;
            current.soundV += 1;
        });
        break;
    case 'cannot':
        change((current) => {
            current.soundBotOk = false;
            current.soundV += 1;
        });
        break;
    case 'empty':
        change((current) => {
            current.sounds = [];
            current.soundV += 1;
        });
        break;
    case 'yours_full':
        change((current) => {
            current.sounds = filledBoard(24, 16);
            current.soundV += 1;
        });
        break;
    case 'board_full':
        change((current) => {
            current.sounds = filledBoard(120, 4);
            current.soundV += 1;
        });
        break;
    default:
        break;
    }
    await pause(600);

    return which;
}

// A file somebody might pick: the lab's decoder makes its shape up (a second
// of quiet, then the drop). A sound the board does not have yet, so the one
// added is plainly new. One called "silence" sounds silent, one called
// "broken" cannot be read, and the big one is over the 4 MB limit.
type LabFile = 'mic-drop' | 'silence' | 'broken' | 'big';

function labFile(kind: LabFile): File {
    if (kind === 'big') {
        return new File([new Uint8Array((4 * 1024 * 1024) + 1)], 'whole-album.flac', {type: 'audio/flac'});
    }

    return new File([new Uint8Array(48213)], kind === 'mic-drop' ? 'mic-drop.mp3' : `${kind}.wav`, {type: kind === 'mic-drop' ? 'audio/mpeg' : 'audio/wav'});
}

// A file given to the add flow's chooser, as a person picking one does.
function pickFile(file: File): boolean {
    const input = document.querySelector<HTMLInputElement>(`${SHEET} input[type="file"]`);
    if (!input) {
        return false;
    }
    const transfer = new DataTransfer();
    transfer.items.add(file);
    input.files = transfer.files;
    input.dispatchEvent(new Event('change', {bubbles: true}));

    return true;
}

// Typing into one of the panel's own fields, as React hears it.
function typeInto(input: HTMLInputElement | null, value: string): void {
    if (!input) {
        return;
    }
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
    setter?.call(input, value);
    input.dispatchEvent(new Event('input', {bubbles: true}));
}

function clickIn(selector: string): boolean {
    const target = document.querySelector<HTMLElement>(`${SHEET} ${selector}`);
    target?.click();

    return Boolean(target);
}

function clickSource(title: string): boolean {
    const row = Array.from(document.querySelectorAll<HTMLButtonElement>(`${SHEET} .vntv-snd-source`)).
        find((button) => button.textContent?.includes(title));
    row?.click();

    return Boolean(row);
}

export type AddStep = 'source' | 'unsupported' | 'record' | 'recording' | 'refused' | 'clips' | 'trim' | 'trim-clip' | 'finish' | 'emoji' |
    'emoji-search' | 'making' | 'added' | 'too_big' | 'silent' | 'refused-add';

export const ADD_STEPS: AddStep[] = ['source', 'unsupported', 'record', 'recording', 'refused', 'clips', 'trim', 'trim-clip', 'finish', 'emoji',
    'emoji-search', 'making', 'added', 'too_big', 'silent', 'refused-add'];

// Each step of adding a sound, reached by pressing the panel's own buttons
// from the board: the add tile, a way in, a file given to the chooser, Next,
// the emoji, the name, Add to the board.
async function addStep(step: AddStep): Promise<string> {
    await soundState('idle');
    if (step === 'unsupported') {
        change((current) => {
            current.soundEncoder = 'none';
        });
    }
    if (step === 'refused') {
        setLabMicrophone('refused');
    }
    if (step === 'refused-add') {
        change((current) => {
            current.soundAddAnswer = 'too_loud';
        });
    }
    if (!clickIn('.vntv-snd-tile[aria-label="Add a sound"]')) {
        return 'no add tile';
    }
    await pause(400);
    if (step === 'source' || step === 'unsupported') {
        return step;
    }

    if (step === 'record' || step === 'recording' || step === 'refused') {
        clickSource('Record');
        await pause(300);
        if (step !== 'record') {
            clickIn('.vntv-snd-rec-button');
            await pause(step === 'recording' ? 2600 : 400);
        }

        return step;
    }

    if (step === 'clips' || step === 'trim-clip') {
        clickSource('From a clip');
        await pause(500 + getWorld().latencyMs);
        if (step === 'trim-clip') {
            clickIn('.vntv-snd-clip[aria-label*="landing"]');
            await pause(700);
        }

        return step;
    }

    let file: LabFile = 'mic-drop';
    if (step === 'too_big') {
        file = 'big';
    } else if (step === 'silent') {
        file = 'silence';
    }
    pickFile(labFile(file));
    await pause(700);
    if (step === 'trim' || step === 'too_big') {
        return step;
    }

    clickIn('.vntv-snd-row .vntv-sheet-primary');
    await pause(300);
    typeInto(document.querySelector<HTMLInputElement>(`${SHEET} .vntv-snd-name-field input`), step === 'silent' ? 'Shh' : 'Mic drop');
    await pause(150);
    if (step === 'finish') {
        return step;
    }
    if (step === 'emoji' || step === 'emoji-search') {
        clickIn('.vntv-snd-emoji-button');
        await pause(300);
        if (step === 'emoji-search') {
            typeInto(document.querySelector<HTMLInputElement>(`${SHEET} .vntv-snd-chooser input`), 'mic');
            await pause(500);
        }

        return step;
    }

    // Choosing the microphone emoji first, from the chooser's search.
    clickIn('.vntv-snd-emoji-button');
    await pause(300);
    typeInto(document.querySelector<HTMLInputElement>(`${SHEET} .vntv-snd-chooser input`), 'microphone');
    await pause(400);
    clickIn('.vntv-snd-pick[aria-label=":microphone:"]');
    await pause(300);

    if (step === 'making') {
        holdClock(true);
        change((current) => {
            current.latencyMs = 4000;
        });
    }
    clickIn('.vntv-snd-submit');
    await pause(step === 'added' ? 1100 + getWorld().latencyMs : 200);
    if (step === 'making') {
        change((current) => {
            current.latencyMs = 180;
        });
    }

    return step;
}

// One of your own sounds' choices, opened as its dots open them.
async function soundMenu(name: string): Promise<string> {
    await soundState('idle');
    const cell = Array.from(document.querySelectorAll<HTMLElement>(`${SHEET} .vntv-snd-cell`)).
        find((each) => each.querySelector(`.vntv-snd-tile[aria-label="Play ${name}"]`));
    cell?.querySelector<HTMLButtonElement>('.vntv-snd-more')?.click();
    await pause(250);

    return cell ? name : 'none';
}

// The new tile's one pulse, held at a moment of it, so a still shows it.
function holdPulse(atMs: number): number {
    const faces = Array.from(document.querySelectorAll<HTMLElement>(`${SHEET} .vntv-snd-tile[data-fresh="true"] .vntv-snd-face`));
    const held = faces.flatMap((face) => face.getAnimations());
    held.forEach((animation) => {
        animation.pause();
        animation.currentTime = atMs;
    });

    return held.length;
}

// Somebody else's sound starting, as the server's sound event says.
function othersSound(ms: number): void {
    const until = clockNow() + ms;
    change((current) => {
        current.soundBusyUntil = until;
    });
    heardSound({busyUntil: until}, clockNow());
}

// ---- @names (8.7) ----

function namesNight(): void {
    liveWith(['Erin', 'Bob', 'Dave'], {Erin: NAMES_TITLE, Bob: UNKNOWN_TITLE}, NAMES_TITLE);
}

function namesLast(): void {
    liveWith(['Erin', 'Bob', 'Dave'], TRAILING_TITLES, TRAILING_TITLES.Erin);
}

function noNames(): void {
    liveWith(['Dave', 'Bob', 'Erin'], NO_NAME_TITLES, NO_NAME_TITLES.Dave);
}

// Erin live on Twitch with an @handle in her Twitch title: no mention.
function twitchHandle(): void {
    change((current) => {
        current.twitchSwitch = true;
        current.twitchCredentials = true;
        current.twitchLive = {
            ...current.twitchLive,
            'u-erin': {title: TWITCH_HANDLE_TITLE, category: 'Assetto Corsa Competizione', viewers: 1234, startedAt: new Date(Date.now() - (52 * 60000)).toISOString()},
        };
    });
}

// The panel draws from the snapshot, so a new one redraws the mentions.
function setLookup(next: Partial<{mode: MentionMode; names: NameDisplay}>): void {
    setMentionSettings(next);
    setSnapshot(snapshotOf());
}

// ---- The channel gallery: plan cards and clip cards (6.5, 9.7) ----

export type Gallery = '' | 'plan' | 'thumb' | 'shared';

const gallery = {which: '' as Gallery, rhs: 0, centre: 0, only: [] as string[]};
const galleryListeners = new Set<() => void>();

function setGallery(next: Partial<typeof gallery>): void {
    Object.assign(gallery, next);
    if (next.which === 'plan') {
        cardPlans();
    }
    galleryListeners.forEach((listener) => listener());
}

export function useGallery(): Gallery {
    useBump(galleryListeners);

    return gallery.which;
}

const NAME_OF: Record<string, string> = Object.fromEntries(LAB_USERS.map((user) => [user.id, user.first_name || user.nickname || user.username || user.id]));

// Mattermost's post time, twelve-hour as its default is.
function postTime(at: number): string {
    return new Intl.DateTimeFormat('en-US', {hour: 'numeric', minute: '2-digit', hour12: true}).format(new Date(at));
}

type Sample = {
    key: string;
    caption: string;
    by: string;
    agoMin?: number;
    at?: number;
    body: React.ReactNode;

    // Mattermost's own reactions under the post, drawn with the system's
    // emoji font; on a server they are Mattermost's images.
    reactions?: Array<{emoji: string; count: number; mine?: boolean}>;
};

const MmPost: React.FC<{sample: Sample}> = ({sample}) => (
    <div className='lab-mm-item'>
        <p className='lab-mm-caption'>{sample.caption}</p>
        <div
            className='lab-mm-post'
            data-shot={sample.key}
        >
            <Avatar
                className='lab-mm-avatar'
                userId={sample.by}
                name={NAME_OF[sample.by] || sample.by}
                decorative={true}
            />
            <div className='lab-mm-main'>
                <div className='lab-mm-head'>
                    <span className='lab-mm-name'>{NAME_OF[sample.by] || sample.by}</span>
                    <span className='lab-mm-badges'>
                        <PostAwardBadges post={{user_id: sample.by}}/>
                    </span>
                    <span className='lab-mm-time'>{postTime(sample.at ?? (Date.now() - ((sample.agoMin || 0) * 60000)))}</span>
                </div>
                <div className='lab-mm-body'>{sample.body}</div>
                {sample.reactions ? (
                    <div className='lab-mm-reactions'>
                        {sample.reactions.map((item) => (
                            <span
                                key={item.emoji}
                                className='lab-mm-reaction'
                                data-mine={item.mine ? 'true' : undefined}
                            >
                                <span className='lab-mm-reaction-emoji'>{item.emoji}</span>
                                {item.count}
                            </span>
                        ))}
                    </div>
                ) : null}
            </div>
        </div>
    </div>
);

type CardKey = keyof typeof CARD_PLANS;

function planPost(key: CardKey): {id: string; type: string; message: string; props: Record<string, unknown>} {
    const id = CARD_PLANS[key];

    return {id: `lab-plan-${key}`, type: PLAN_POST_TYPE, message: labPlanShareMessage(id), props: labPlanShareProps(id, cardStartOf(key))};
}

function clipPost(id: string): {id: string; type: string; message: string; props: Record<string, unknown>} | null {
    const entry = getWorld().clips.find((clip) => clip.id === id);

    return entry ? {id: `lab-clip-${id}`, type: CLIP_POST_TYPE, message: labShareMessage(entry), props: {...labShareProps(entry), villanet_share_nonce: ''}} : null;
}

// The waiting face: the same post read before any snapshot has arrived,
// which the page cannot be made to be again once it has one.
const WaitingCard: React.FC = () => {
    const layout = useCardLayout();
    const view = planCardView({post: planPost('upcoming'), schedule: null, liveHandles: {}, reader: 'linked', meUserId: ME_ID, now: Date.now(), zone: viewerZone(), name: nameNow()});

    return (
        <PlanCardFace
            view={view}
            compact={layout.compact}
        />
    );
};

function planSamples(): Sample[] {
    const body = (key: CardKey) => <LabPostBody post={planPost(key)}/>;

    return [
        {key: 'waiting', caption: 'Waiting: no snapshot yet', by: 'u-bob', agoMin: 2, body: <WaitingCard/>},
        {key: 'upcoming', caption: 'Upcoming: Dave’s Arma co-op, Bob and Erin in', by: 'u-bob', agoMin: 3, body: body('upcoming')},
        {key: 'due', caption: 'Due: Bob’s Valheim build night, its time come, Bob not on yet', by: 'u-dave', agoMin: 40, body: body('due')},
        {key: 'live', caption: 'Live: Erin’s Hytale first look, Erin on the air', by: 'u-dave', agoMin: 14, body: body('live')},
        {key: 'in', caption: 'In: Dave’s Arma Reforger night, Victor in', by: 'u-dave', agoMin: 50, body: body('in')},
        {key: 'full', caption: 'Full: Caro’s Star Citizen fleet night, twelve in', by: 'u-erin', agoMin: 70, body: body('full')},
        {key: 'mine', caption: 'Your own plan (Victor’s Film night)', by: ME_ID, agoMin: 20, body: body('mine')},
        {key: 'ended', caption: 'Ended: started 5 hours ago, off the list', by: 'u-erin', agoMin: 400, body: body('ended')},
        {key: 'cancelled', caption: 'Cancelled: taken off the list before its time', by: 'u-dave', agoMin: 90, body: body('cancelled')},
    ];
}

// The plan posts the lab's shares made in this page, oldest first: the Up
// next tool's and the plan sheet's offer's.
function sharedSamples(posts: Record<string, LabPost>): Sample[] {
    return Object.values(posts).
        filter((post) => postTypeOf(post) === PLAN_POST_TYPE && post.root_id === '' && !post.id.startsWith('lab-')).
        sort((a, b) => a.create_at - b.create_at).
        map((post) => ({key: `shared-${post.id}`, caption: 'Shared from the panel', by: post.user_id, at: post.create_at, body: <LabPostBody post={post}/>}));
}

function thumbSamples(): Sample[] {
    const samples: Sample[] = [];
    const add = (key: string, caption: string, by: string, agoMin: number, id: string, reactions?: Sample['reactions']) => {
        const post = clipPost(id);
        if (post) {
            samples.push({key, caption, by, agoMin, body: <LabPostBody post={post}/>, reactions});
        }
    };

    add('clip-open', 'Victor has not given it a thumbs up', 'u-bob', 6, '5d6e7f8091a2b3c4');
    add('clip-voted', 'Victor gave it a thumbs up; Mattermost’s own reactions under it', 'u-dave', 12, '3f9a0c2e7b1d4a55',
        [{emoji: '👍', count: 2, mine: true}, {emoji: '🔥', count: 1}]);
    add('clip-own', 'Victor’s own clip', ME_ID, 30, 'a1b2c3d4e5f60718');

    return samples;
}

// The gallery, in place of the channel's messages while it is on, in the
// theme the Shared clips block sets.
export const LabGallery320: React.FC = () => {
    const which = useGallery();
    const posts = useLabState().entities.posts.posts as Record<string, LabPost>;
    useLabWorld();
    useCardLayout();

    if (which === '') {
        return null;
    }

    let samples: Sample[] = [];
    if (which === 'plan') {
        samples = planSamples();
    } else if (which === 'thumb') {
        samples = thumbSamples();
    } else {
        samples = sharedSamples(posts);
    }
    const width = gallery.rhs || gallery.centre;

    return (
        <div
            className='lab-mm-frame'
            style={width > 0 ? {width: `${width}px`} : undefined}
            data-rhs={gallery.rhs > 0 ? 'true' : undefined}
        >
            <LabCentre>
                <div
                    className='lab-mm-list'
                    data-gallery={which}
                >
                    {samples.filter((sample) => gallery.only.length === 0 || gallery.only.includes(sample.key)).map((sample) => (
                        <MmPost
                            key={sample.key}
                            sample={sample}
                        />
                    ))}
                </div>
            </LabCentre>
        </div>
    );
};

// The Up next cards' tools shown as on a touch screen, so a still picture can
// show them without a pointer over the card.
let toolsShown = false;

function showCardTools(on: boolean): void {
    toolsShown = on;
    document.querySelector('.lab')?.setAttribute('data-tools-shown', on ? 'true' : 'false');
}

// The plan sheet's offer: a plan saved from the sheet, as a person saves one.
async function planAndOffer(title: string): Promise<string> {
    const tool = document.querySelector<HTMLButtonElement>('button[aria-label="Plan a stream"]');
    if (!tool) {
        return 'no plan tool: is Victor linked?';
    }
    if (tool.getAttribute('aria-expanded') !== 'true') {
        tool.click();
        await pause(450);
    }
    typeInto(document.querySelector<HTMLInputElement>('.vntv-sched-form input'), title);
    await pause(250);
    document.querySelector<HTMLButtonElement>('.vntv-sched-form .vntv-field-save')?.click();
    await pause(500 + getWorld().latencyMs);

    return document.querySelector('.vntv-sched-saved') ? 'offered' : 'no offer';
}

// ---- The stage: Mattermost's post header and profile popover ----

// A post in Mattermost's centre channel, its header as Mattermost lays it
// out: the name, the plugin's badges, the time; then the message.
const LabPostHeader: React.FC<{userId: string; theme: CentreTheme}> = ({userId, theme}) => {
    const vars = THEMES[theme];
    const user = LAB_USERS.find((candidate) => candidate.id === userId);

    return (
        <div
            className='lab-p320-post'
            data-theme={theme}
            style={{...vars, background: vars['--center-channel-bg'], color: vars['--center-channel-color']} as React.CSSProperties}
        >
            <div className='lab-p320-post-head'>
                <span className='lab-p320-post-name'>{user ? displayNameOf(user, 'full') : userId}</span>
                <span className='lab-p320-post-badges'>
                    <PostAwardBadges post={{user_id: userId}}/>
                    <span className='lab-p320-post-time'>{'9:41 PM'}</span>
                </span>
            </div>
            <div className='lab-p320-post-body'>{'Who’s on for Dave’s op tonight?'}</div>
        </div>
    );
};

const stage = {profile: ME_ID, post: 'u-dave'};
const stageListeners = new Set<() => void>();

function setStage(next: Partial<typeof stage>): void {
    Object.assign(stage, next);
    stageListeners.forEach((listener) => listener());
}

const PEOPLE = LAB_USERS.slice(0, 4);

// ---- The rows ----

export const Rows320: React.FC<{Row: React.FC<RowProps>}> = ({Row}) => {
    const world = useLabWorld();
    const layout = useCardLayout();
    const which = useGallery();
    useBump(stageListeners);
    const mention = mentionSettings();
    const [, bump] = React.useState(0);
    const set = choiceSetOf(world);

    return (
        <div className='lab-p320'>
            <h2 className='lab-subtitle'>{'3.2.0'}</h2>
            <p className='lab-blurb'>{'Awards by your name, biggest fans, achievements, the soundboard, shared plans, @names and the thumb (design/3.2.0.md 11.3).'}</p>

            <Row label='Choices'>
                <Chip
                    pressed={set === 'auto'}
                    title='Nobody has chosen: every badge beside a name is exactly 3.1.0’s'
                    onClick={() => chooseSet('auto')}
                >{'Automatic'}</Chip>
                <Chip
                    pressed={set === 'everybody'}
                    title='3.9: Dave this month and Biggest fan of Bob; Bob nothing; Victor the binoculars (Erin’s, so lost), the crown and Podium; Erin her past months'
                    onClick={() => chooseSet('everybody')}
                >{'everybody chose'}</Chip>
                <Chip
                    pressed={set === 'four'}
                    title='Victor chose Streamed, the crown, his past months and Biggest fan of Erin, in that order'
                    onClick={() => chooseSet('four')}
                >{'Victor’s four'}</Chip>
                <Chip
                    pressed={!world.awardsReadable}
                    title='award_prefs could not be read: everybody is Automatic, and the picker says so'
                    onClick={() => change((current) => {
                        current.awardsReadable = !current.awardsReadable;
                    })}
                >{'unreadable'}</Chip>
                <Chip
                    title='The picker marks Victor’s first Lo-fi radio hour NEW next time it opens'
                    onClick={seedNewAward}
                >{'NEW: Lo-fi radio'}</Chip>
                {(['you', 'achievements', 'watchers', 'streamers'] as const).map((tab) => (
                    <Chip
                        key={tab}
                        title={`The Trophies card, on its ${tab} tab`}
                        onClick={() => void openTrophies(tab)}
                    >{`Trophies: ${tab}`}</Chip>
                ))}
            </Row>

            <Row label='Fans'>
                <Chip
                    pressed={world.fansOn}
                    title='EnableBiggestFans (with Top Snoopers on: the Snoopers row)'
                    onClick={() => change((current) => {
                        current.fansOn = !current.fansOn;
                    })}
                >{'counted'}</Chip>
                <Chip
                    pressed={world.fanHours.length === 0}
                    title='On, and nobody past an hour yet'
                    onClick={() => change((current) => {
                        current.fanHours = current.fanHours.length === 0 ? startingFanHours() : [];
                    })}
                >{'nobody yet'}</Chip>
                <Chip
                    pressed={Boolean(world.ledgerAlert.fansResetAt)}
                    title='The fan hours could not be read and were started again from zero (admins)'
                    onClick={() => setLedgerReset('fansResetAt', !world.ledgerAlert.fansResetAt)}
                >{'fan hours reset'}</Chip>
                <Chip
                    pressed={Boolean(world.ledgerAlert.channelsResetAt)}
                    title='The 24/7 channel hours could not be read and were started again from zero (admins)'
                    onClick={() => setLedgerReset('channelsResetAt', !world.ledgerAlert.channelsResetAt)}
                >{'channel hours reset'}</Chip>
                <Chip
                    title='Dave, Bob, Erin and Victor live'
                    onClick={() => liveWith(['Dave', 'Bob', 'Erin', 'Victor'])}
                >{'four live'}</Chip>
            </Row>

            <Row label='Achievements'>
                <Chip
                    pressed={world.achievementsOn}
                    title='EnableAchievements'
                    onClick={() => change((current) => {
                        current.achievementsOn = !current.achievementsOn;
                    })}
                >{'on'}</Chip>
                <Chip
                    title='Victor’s snooped hours pass 100: the toast over his bar'
                    onClick={() => earnStep('snooped', 100)}
                >{'toast: Snooped 100 h'}</Chip>
                <Chip
                    title='Victor’s streamed hours pass 500'
                    onClick={() => earnStep('streamed', 500)}
                >{'toast: Streamed 500 h'}</Chip>
                <Chip
                    title='Everybody’s figures as the lab starts'
                    onClick={() => change((current) => {
                        current.achievementValues = startingAchievementValues();
                    })}
                >{'reset figures'}</Chip>
                <Chip
                    pressed={world.achievementsAlert !== ''}
                    title='The ChannelAchievements setting has a line the server cannot read (admins)'
                    onClick={() => change((current) => {
                        current.achievementsAlert = current.achievementsAlert ? '' : 'line 2: “Lofi Radio” is not a 24/7 channel’s name.';
                    })}
                >{'24/7 setting fault'}</Chip>
            </Row>

            <Row label='Soundboard'>
                <Chip
                    pressed={world.soundboardOn}
                    title='EnableSoundboard (off by default on a server)'
                    onClick={() => change((current) => {
                        current.soundboardOn = !current.soundboardOn;
                    })}
                >{'on'}</Chip>
                {SOUND_STATES.map((state) => (
                    <Chip
                        key={state}
                        onClick={() => void soundState(state)}
                    >{state}</Chip>
                ))}
                <Chip
                    title='Your own sound’s choices: Listen here, Rename, Change emoji, Delete'
                    onClick={() => void soundMenu('Ba dum tss')}
                >{'⋯ menu'}</Chip>
                <Chip
                    title='Somebody else’s sound starts: the tiles wait for it'
                    onClick={() => othersSound(2600)}
                >{'Dave plays one'}</Chip>
                <Chip
                    pressed={world.soundBlocked.some((person) => person.userId === ME_ID)}
                    title='An admin blocked Victor from the soundboard'
                    onClick={() => change((current) => {
                        const blocked = current.soundBlocked.some((person) => person.userId === ME_ID);
                        current.soundBlocked = blocked ?
                            current.soundBlocked.filter((person) => person.userId !== ME_ID) :
                            current.soundBlocked.concat({userId: ME_ID, username: 'victor', name: 'Victor', by: 'Dave', at: Date.now()});
                    })}
                >{'Victor blocked'}</Chip>
                <Chip
                    pressed={world.soundAlert !== ''}
                    title='The plugin’s Mumble account cannot speak: the admins’ line'
                    onClick={() => change((current) => {
                        current.soundAlert = current.soundAlert ? '' : 'The soundboard can’t play: Matty’s Mumble account is muted on the Mumble server.';
                        current.soundBotOk = current.soundAlert === '';
                        current.soundV += 1;
                    })}
                >{'admin line'}</Chip>
                <Chip
                    pressed={!world.soundsReadable}
                    title='The sounds document could not be read (the System Console card offers Start the board again)'
                    onClick={() => change((current) => {
                        current.soundsReadable = !current.soundsReadable;
                    })}
                >{'sounds unreadable'}</Chip>
            </Row>

            <Row label='Add a sound'>
                {ADD_STEPS.map((step) => (
                    <Chip
                        key={step}
                        onClick={() => void addStep(step)}
                    >{step}</Chip>
                ))}
            </Row>

            <Row label='Plans'>
                {([['', 'chat'], ['plan', 'plan cards'], ['thumb', 'clip cards'], ['shared', 'shared plans']] as Array<[Gallery, string]>).map(([kind, label]) => (
                    <Chip
                        key={kind || 'chat'}
                        pressed={which === kind}
                        title={kind === '' ? 'The channel’s own messages' : `Every state of the ${label}, as posts in the channel`}
                        onClick={() => setGallery({which: kind})}
                    >{label}</Chip>
                ))}
                {(['denim', 'onyx'] as CentreTheme[]).map((theme) => (
                    <Chip
                        key={theme}
                        pressed={layout.theme === theme}
                        onClick={() => setCardLayout({theme})}
                    >{theme === 'denim' ? 'Denim (light)' : 'Onyx (dark)'}</Chip>
                ))}
                <Chip
                    pressed={layout.compact}
                    title='Mattermost’s compact message display'
                    onClick={() => setCardLayout({compact: !layout.compact})}
                >{'compact'}</Chip>
                <Chip
                    pressed={gallery.rhs === 304}
                    title='The cards as a thread in Mattermost’s side panel 304px wide shows them'
                    onClick={() => setGallery({rhs: gallery.rhs === 304 ? 0 : 304})}
                >{'304px thread'}</Chip>
                <Chip
                    pressed={toolsShown}
                    title='The Up next cards’ tools (share, pencil, ×) shown as on a touch screen'
                    onClick={() => {
                        showCardTools(!toolsShown);
                        bump((value) => value + 1);
                    }}
                >{'card tools shown'}</Chip>
                <Chip
                    title='Plan “Arma co-op” from the plan sheet: the offer to share it'
                    onClick={() => void planAndOffer('Arma co-op')}
                >{'plan sheet offer'}</Chip>
            </Row>

            <Row label='@names'>
                {([['resolved', 'known', 'Everybody already in Mattermost’s store'], ['slow', 'after 500 ms', 'Looked up: as typed first, then as Mattermost names them'], ['held', 'held', 'The lookup never answers: names stay as typed']] as Array<[MentionMode, string, string]>).map(([value, label, title]) => (
                    <Chip
                        key={value}
                        pressed={mention.mode === value}
                        title={title}
                        onClick={() => {
                            setLookup({mode: value});
                            bump((count) => count + 1);
                        }}
                    >{label}</Chip>
                ))}
                {(['full', 'username'] as NameDisplay[]).map((value) => (
                    <Chip
                        key={value}
                        pressed={mention.names === value}
                        title={value === 'full' ? 'Teammate Name Display: full name' : 'Teammate Name Display: username'}
                        onClick={() => {
                            setLookup({names: value});
                            bump((count) => count + 1);
                        }}
                    >{value === 'full' ? 'full names' : 'usernames'}</Chip>
                ))}
                <Chip
                    title={`Erin: “${NAMES_TITLE}”; Bob: “${UNKNOWN_TITLE}”; Victor’s own title the first`}
                    onClick={namesNight}
                >{'names'}</Chip>
                <Chip
                    title='Titles ending in a name'
                    onClick={namesLast}
                >{'names last'}</Chip>
                <Chip
                    title='@all, an address and a link: no mention in any'
                    onClick={noNames}
                >{'no names'}</Chip>
                <Chip
                    title='Erin on Twitch, an @handle in her Twitch title: no mention'
                    onClick={twitchHandle}
                >{'Twitch @handle'}</Chip>
            </Row>

            <Row label='Mattermost'>
                {PEOPLE.map((user) => (
                    <Chip
                        key={user.id}
                        pressed={stage.profile === user.id}
                        title='Whose profile popover is below (Victor’s is his own, with the takebacks)'
                        onClick={() => setStage({profile: user.id})}
                    >{`profile: ${user.first_name}`}</Chip>
                ))}
                {PEOPLE.map((user) => (
                    <Chip
                        key={`post-${user.id}`}
                        pressed={stage.post === user.id}
                        title='Whose post header is below'
                        onClick={() => setStage({post: user.id})}
                    >{`post: ${user.first_name}`}</Chip>
                ))}
            </Row>

            <div className='lab-p320-stage'>
                {(['denim', 'onyx'] as CentreTheme[]).map((theme) => (
                    <div
                        key={theme}
                        className='lab-p320-col'
                        data-theme={theme}
                    >
                        <LabPostHeader
                            userId={stage.post}
                            theme={theme}
                        />
                        <LabProfileCard
                            userId={stage.profile}
                            theme={theme}
                        />
                    </div>
                ))}
            </div>
        </div>
    );
};

// For window.__villanetLab.v320: the screenshots' handles.
export const labHooks320 = {
    choices: chooseSet,
    seedNew: seedNewAward,
    earn: earnStep,
    trophies: openTrophies,
    ledgerReset: setLedgerReset,
    sound: soundState,
    add: addStep,
    menu: soundMenu,
    othersSound,
    holdPulse,
    holdClock,
    advanceClock,
    openSounds,
    closeSounds,
    microphone: setLabMicrophone,
    gallery: (which: Gallery) => setGallery({which}),
    galleryRhs: (px: number) => setGallery({rhs: px}),
    galleryCentre: (px: number) => setGallery({centre: px}),
    galleryOnly: (keys: string[]) => setGallery({only: keys}),
    theme: (theme: CentreTheme) => setCardLayout({theme}),
    compact: (on: boolean) => setCardLayout({compact: on}),
    cardPlans,
    tools: showCardTools,
    planAndOffer,
    lookup: (mode: MentionMode) => setLookup({mode}),
    nameDisplay: (names: NameDisplay) => setLookup({names}),
    namesNight,
    namesLast,
    noNames,
    twitchHandle,
    live: liveWith,
    stage: setStage,
    boxes: (): Record<string, [number, number, number, number]> => {
        const out: Record<string, [number, number, number, number]> = {};
        document.querySelectorAll<HTMLElement>('[data-shot]').forEach((element) => {
            const box = element.getBoundingClientRect();
            out[element.dataset.shot || ''] = [box.x, box.y, box.width, box.height];
        });

        return out;
    },
    rect: (selector: string): [number, number, number, number] | null => {
        const box = document.querySelector(selector)?.getBoundingClientRect();

        return box ? [box.x, box.y, box.width, box.height] : null;
    },
};
