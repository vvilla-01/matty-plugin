import React from 'react';

import {addSound, editSound, fetchClips, SoundRequestError} from '../client';
import {agoWords, clipTime, streamLabel} from '../clipLogic';
import {shownTitleOf} from '../clips';
import {now as clockNow} from '../clock';
import {nameNow, useName} from '../name';
import {decodeClip, decodeFile, FILE_LIMIT, inDesktopApp, canMakeSounds, listenHere, makeSound, RECORD_MS, SoundMakeError, startRecording} from '../soundEncode';
import type {Decoded, Playing, Recording} from '../soundEncode';
import {useMoreBelow} from '../moreBelow';
import {DEFAULT_EMOJI, SOUND_MAX_MS, SOUND_NAME_MAX, clampTrim, nameLeft, refusalLine, seconds} from '../soundLogic';
import type {Clip, Sound, SoundBoard, SoundEmoji} from '../types';

import {ClipPicture} from './ClipCard';
import {EmojiChooser, SoundEmojiImage} from './EmojiChooser';
import {InfoIcon, MicIcon, PauseIcon, PlayIcon, ScissorsIcon, StopIcon, UploadIcon} from './icons';
import {SheetHead} from './SheetHead';

// Adding a sound (design/3.2.0.md 7.13, and Victor's answer to question 10):
// in the soundboard's own sheet, in place of the grid, with Back to return.
//
// Three ways in — a sound file, the microphone, or one of the panel's clips — and one
// way on from each: the same trim (up to six seconds, the clip trimmer's
// handles over the sound's own shape), then an emoji and a name with the tile
// drawn as it will be on the board, then ADD TO THE BOARD. All three end in
// the same decoded sound, levelled and encoded in this browser (soundEncode.ts),
// so the server cannot tell them apart. Upload goes straight from the file
// chooser to the trim; Record and From a clip have a step of their own first.
//
// Renaming one of your own sounds, or giving it another emoji, is the last two
// steps of the same flow, with SAVE in place of ADD TO THE BOARD.

type Source = 'file' | 'record' | 'clip';

// Where the flow opens: the three ways in, or one of them straight away (the
// empty board's invitation), or for a sound of your own, its name or its
// emoji.
export type AddFrom = 'source' | Source | 'finish' | 'emoji';

// What is being made, gathered step by step.
type Draft = {
    source: Source;

    // The file's name, or the clip's title, for the heads.
    label: string;
    clip?: Clip;
    decoded: Decoded | null;
    inMs: number;
    outMs: number;
    emoji: SoundEmoji;
    name: string;
};

type Step = 'source' | 'record' | 'clips' | 'trim' | 'finish' | 'emoji';

type RecordState = {phase: 'ready' | 'asking' | 'recording' | 'refused'; levels: number[]; ms: number};

const ACCEPT = 'audio/*,.mp3,.wav,.ogg,.m4a,.flac,.webm';

export const UNSUPPORTED = 'This browser can’t make sounds. Use the Mattermost desktop app, Chrome or Edge 119 or later, Firefox 130 or later, or Safari 26 or later.';

// Where the focus goes as each step opens: its first control.
const STEP_FOCUS: Record<Step, string> = {
    source: '.vntv-snd-source',
    record: '.vntv-snd-rec-button',
    clips: '.vntv-snd-clip-pick input, .vntv-snd-clip',
    trim: '.vntv-trim-thumb-start',
    finish: '.vntv-snd-name-field input',
    emoji: '.vntv-snd-chooser input',
};

const blankDraft = (source: Source): Draft => ({
    source, label: '', decoded: null, inMs: 0, outMs: 0, emoji: DEFAULT_EMOJI, name: '',
});

// The window starts at the first loud moment, and runs up to six seconds.
function firstWindow(decoded: Decoded): {inMs: number; outMs: number} {
    const loud = decoded.peaks.findIndex((peak) => peak > 0.5);
    const start = loud > 0 ? Math.max(0, ((loud - 1) / decoded.peaks.length) * decoded.durationMs) : 0;

    return clampTrim(start, start + Math.min(SOUND_MAX_MS, decoded.durationMs), decoded.durationMs, 'out');
}

// With the server's name for the plugin (name.ts), for the clips' row.
const sourcesOf = (name: string): Array<{source: Source; title: string; words: string; icon: React.ReactNode}> => [
    {source: 'file', title: 'Upload a file', words: 'MP3, WAV, OGG, M4A, FLAC or WebM, up to 4 MB', icon: <UploadIcon size={17}/>},
    {source: 'record', title: 'Record', words: 'Up to 6 seconds from your microphone', icon: <MicIcon size={17}/>},
    {source: 'clip', title: 'From a clip', words: `Cut it from a clip in ${name}`, icon: <ScissorsIcon size={17}/>},
];

// The three ways in, as rows: on the first step, and on an empty board as its
// invitation.
export const SoundSources: React.FC<{onPick: (source: Source) => void}> = ({onPick}) => {
    const name = useName();

    return (
        <div
            className='vntv-snd-sources'
            role='group'
            aria-label='Add a sound from'
        >
            {sourcesOf(name).map(({source, title, words, icon}) => (
                <button
                    key={source}
                    type='button'
                    className='vntv-snd-source'
                    onClick={() => onPick(source)}
                >
                    <span
                        className='vntv-snd-source-icon'
                        aria-hidden={true}
                    >{icon}</span>
                    <span className='vntv-snd-source-words'>
                        <span className='vntv-snd-source-title'>{title}</span>
                        <span className='vntv-snd-source-line'>{words}</span>
                    </span>
                </button>
            ))}
        </div>
    );
};

// What a refused add, rename or emoji change says (7.14).
function problemOf(reason: unknown): string {
    if (reason instanceof SoundMakeError) {
        return reason.reason === 'silent' ? 'That sounds silent.' : UNSUPPORTED;
    }
    if (reason instanceof SoundRequestError) {
        return refusalLine(reason.code, nameNow(), reason.retryAfterMs);
    }

    return refusalLine('store', nameNow());
}

type Props = {
    from: AddFrom;

    // A sound of your own being renamed or given another emoji: straight to
    // the name (from 'finish') or the chooser (from 'emoji').
    editing?: Sound;
    limits?: SoundBoard['limits'];
    onBack: () => void;
    onClose: () => void;
    onAdded: (sound: {id: string; name: string}) => void;
};

export const AddSound: React.FC<Props> = ({from, editing, limits, onBack, onClose, onAdded}) => {
    const [step, setStep] = React.useState<Step>(() => {
        if (editing) {
            return from === 'emoji' ? 'emoji' : 'finish';
        }
        if (from === 'record') {
            return 'record';
        }

        return from === 'clip' ? 'clips' : 'source';
    });
    const [draft, setDraft] = React.useState<Draft>(() => (editing ?
        {...blankDraft('file'), emoji: editing.emoji, name: editing.name, outMs: editing.ms} :
        blankDraft('file')));
    const [supported, setSupported] = React.useState<boolean | null>(null);
    const [problem, setProblem] = React.useState('');
    const [working, setWorking] = React.useState('');
    const [rec, setRec] = React.useState<RecordState>({phase: 'ready', levels: [], ms: 0});
    const [clips, setClips] = React.useState<Clip[] | null>(null);
    const [dropping, setDropping] = React.useState(false);
    const rootRef = React.useRef<HTMLDivElement | null>(null);
    const fileRef = React.useRef<HTMLInputElement | null>(null);
    const recording = React.useRef<Recording | null>(null);
    const alive = React.useRef(true);

    // Which recording is the flow's own. Back, and leaving the sheet, move it
    // on, so a recording that answers late — the microphone allowed after the
    // flow was left, or a stopped one decoded after Back — is thrown away,
    // and its microphone closed, rather than starting with nothing on screen
    // or pulling the reader onto the trim.
    const recordingTurn = React.useRef(0);
    const dropRecording = React.useCallback(() => {
        recordingTurn.current += 1;
        recording.current?.cancel();
        recording.current = null;
    }, []);

    React.useEffect(() => {
        void canMakeSounds().then((can) => {
            if (alive.current) {
                setSupported(can);
            }
        });
    }, []);

    // Recording stops with the step, and with the sheet.
    React.useEffect(() => () => {
        alive.current = false;
        dropRecording();
    }, [dropRecording]);

    // A browser that cannot make a sound is told so on the first step and
    // offered nothing else (7.13 step 1), whichever way in was pressed.
    React.useEffect(() => {
        if (supported === false && !editing && step !== 'source') {
            dropRecording();
            setStep('source');
        }
    }, [supported, editing, step, dropRecording]);

    // Each step's first control takes the focus as the step opens, as the
    // control that opened it has just gone; so the keyboard carries on from
    // there, and Escape still reaches the sheet. Not when the reader has
    // gone somewhere else in the page meanwhile.
    const cameFrom = React.useRef<Step | null>(null);
    const waitingFocus = React.useRef<HTMLElement | null>(null);
    React.useEffect(() => {
        const root = rootRef.current;
        const sheet = root?.closest<HTMLElement>('.vntv-sheet');
        const active = document.activeElement;
        const before = cameFrom.current;
        cameFrom.current = step;
        if (!root || !sheet || (active && active !== document.body && !sheet.contains(active))) {
            return;
        }
        if (before === null && editing && step === 'finish' && from !== 'emoji') {
            // Rename: the name field takes it (FinishStep).
            return;
        }

        const wanted = step === 'finish' && before === 'emoji' ? '.vntv-snd-emoji-button' : STEP_FOCUS[step];
        const found = root.querySelector<HTMLElement>(wanted);
        const target = found || root.querySelector<HTMLElement>('.vntv-sheet-back') || sheet;
        waitingFocus.current = found ? null : target;
        target.focus({preventScroll: true});
    }, [step]); // eslint-disable-line react-hooks/exhaustive-deps

    // A step whose first control was not there yet (the clips still on their
    // way) hands the focus on once it is, if it is still where it was put.
    React.useEffect(() => {
        const held = waitingFocus.current;
        const found = held && rootRef.current?.querySelector<HTMLElement>(STEP_FOCUS[step]);
        if (held && found) {
            waitingFocus.current = null;
            if (document.activeElement === held) {
                found.focus({preventScroll: true});
            }
        }
    });

    const toTrim = React.useCallback((next: Draft, decoded: Decoded) => {
        if (!alive.current) {
            return;
        }
        setDraft({...next, decoded, ...firstWindow(decoded)});
        setStep('trim');
    }, []);

    const readFile = React.useCallback((file: File) => {
        setProblem('');
        if (file.size > FILE_LIMIT) {
            setProblem('That file is bigger than 4 MB.');

            return;
        }
        setWorking('Reading it…');
        decodeFile(file).then((decoded) => toTrim({...blankDraft('file'), label: file.name}, decoded)).
            catch(() => alive.current && setProblem('That file couldn’t be read as sound.')).
            finally(() => alive.current && setWorking(''));
    }, [toTrim]);

    const openClips = React.useCallback(() => {
        setStep('clips');
        setProblem('');
        fetchClips().then(({clips: list}) => alive.current && setClips(list)).catch(() => alive.current && setClips([]));
    }, []);

    // The empty board's invitation opens straight onto one way in.
    React.useEffect(() => {
        if (editing) {
            return;
        }
        if (from === 'file') {
            fileRef.current?.click();
        } else if (from === 'clip') {
            openClips();
        }

    // Once, as the flow opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    const pickClip = React.useCallback((clip: Clip) => {
        setProblem('');
        setWorking('Reading its sound…');
        decodeClip(clip.id).then((decoded) => toTrim({...blankDraft('clip'), label: shownTitleOf(clip), clip}, decoded)).
            catch(() => alive.current && setProblem('That clip couldn’t be read as sound.')).
            finally(() => alive.current && setWorking(''));
    }, [toTrim]);

    const record = React.useCallback(() => {
        setProblem('');
        setRec({phase: 'asking', levels: [], ms: 0});
        recordingTurn.current += 1;
        const turn = recordingTurn.current;
        const ours = () => alive.current && turn === recordingTurn.current;
        startRecording(
            (levels, ms) => ours() && setRec({phase: 'recording', levels, ms}),
            (decoded) => {
                if (ours()) {
                    recording.current = null;
                    toTrim({...blankDraft('record'), label: 'Your recording'}, decoded);
                }
            },
            () => {
                if (ours()) {
                    recording.current = null;
                    setRec({phase: 'ready', levels: [], ms: 0});
                    setProblem('That recording couldn’t be read as sound.');
                }
            },
        ).then((started) => {
            if (!ours()) {
                started.cancel();

                return;
            }
            recording.current = started;
        }).catch(() => ours() && setRec({phase: 'refused', levels: [], ms: 0}));
    }, [toTrim]);

    const pick = (source: Source) => {
        setProblem('');
        if (source === 'file') {
            fileRef.current?.click();
        } else if (source === 'record') {
            setStep('record');
        } else {
            openClips();
        }
    };

    const back = () => {
        dropRecording();
        setRec({phase: 'ready', levels: [], ms: 0});
        setProblem('');
        if (editing || step === 'source') {
            onBack();
        } else if (step === 'emoji') {
            setStep('finish');
        } else if (step === 'finish') {
            setStep(draft.decoded ? 'trim' : 'source');
        } else if (step === 'trim' && draft.source === 'clip') {
            setStep('clips');
        } else if (step === 'trim' && draft.source === 'record') {
            setStep('record');
        } else {
            setStep('source');
        }
    };

    const submit = () => {
        const name = draft.name.trim();
        if (working !== '') {
            return;
        }
        if (name === '' || nameLeft(name) < 0) {
            setProblem('Give it a name of 16 characters at most, on one line.');

            return;
        }

        setProblem('');
        const emoji = {kind: draft.emoji.kind, name: draft.emoji.name};
        if (editing) {
            setWorking('Saving…');
            const change = name === editing.name ? {emoji} : {name, emoji};
            editSound(editing.id, change).then(() => onAdded({id: editing.id, name})).
                catch((reason: unknown) => alive.current && setProblem(problemOf(reason))).
                finally(() => alive.current && setWorking(''));

            return;
        }

        const decoded = draft.decoded;
        if (!decoded) {
            return;
        }

        setWorking('Making it…');
        makeSound(decoded, draft.inMs, draft.outMs).then((made) => {
            if (alive.current) {
                setWorking('Adding…');
            }

            return addSound({name, emoji, packets: made.packets});
        }).then(({id}) => onAdded({id, name})).
            catch((reason: unknown) => alive.current && setProblem(problemOf(reason))).
            finally(() => alive.current && setWorking(''));
    };

    const length = seconds(draft.outMs - draft.inMs);
    const heads: Record<Step, {title: string; note?: string}> = {
        source: {title: 'Add a sound', note: 'Up to 6 seconds'},
        record: {title: 'Record a sound'},
        clips: {title: 'From a clip', note: 'Pick the clip, then the moment'},
        trim: {title: 'Trim it', note: draft.clip ? 'Up to 6 seconds of it' : draft.label},
        finish: {title: editing ? `Change ${editing.name}` : 'Name it', note: editing ? undefined : `${length} from ${draft.label}`},
        emoji: {title: 'Pick an emoji'},
    };

    // A file dropped anywhere on the first step is read as if chosen.
    const dropZone = step === 'source' && supported !== false ? {
        onDragOver: (event: React.DragEvent) => {
            event.preventDefault();
            setDropping(true);
        },
        onDragLeave: () => setDropping(false),
        onDrop: (event: React.DragEvent) => {
            event.preventDefault();
            setDropping(false);
            const file = event.dataTransfer.files?.[0];
            if (file) {
                readFile(file);
            }
        },
    } : {};

    return (
        <div
            ref={rootRef}
            className='vntv-snd-add'
            data-dropping={dropping ? 'true' : undefined}
            {...dropZone}
        >
            {/* The head's own arrow back, before the title, as every
                other sheet's step has (Connected accounts, Twitch). */}
            <SheetHead
                title={heads[step].title}
                note={heads[step].note}
                onClose={onClose}
                onBack={back}
                backLabel={step === 'source' || editing ? 'Back to the board' : 'Back'}
            />

            <input
                ref={fileRef}
                className='vntv-sr-only'
                type='file'
                accept={ACCEPT}
                tabIndex={-1}
                aria-hidden={true}
                onChange={(event) => {
                    const file = event.target.files?.[0];
                    event.target.value = '';
                    if (file) {
                        readFile(file);
                    }
                }}
            />

            {step === 'source' && supported === false ? <p className='vntv-snd-why'>{UNSUPPORTED}</p> : null}
            {step === 'source' && supported !== false ? <SoundSources onPick={pick}/> : null}

            {step === 'record' ? (
                <RecordStep
                    state={rec}
                    onRecord={record}
                    onStop={() => recording.current?.stop()}
                />
            ) : null}

            {step === 'clips' ? (
                <ClipPicker
                    clips={clips}
                    onPick={pickClip}
                />
            ) : null}

            {step === 'trim' && draft.decoded ? (
                <>
                    {draft.clip ? <ClipRow clip={draft.clip}/> : null}
                    <SoundTrim
                        decoded={draft.decoded}
                        inMs={draft.inMs}
                        outMs={draft.outMs}
                        onChange={(inMs, outMs) => setDraft((current) => ({...current, inMs, outMs}))}
                    />
                    <div className='vntv-snd-row'>
                        <span className='vntv-snd-hint'>{'Drag the ends to keep up to 6 seconds.'}</span>
                        <button
                            type='button'
                            className='vntv-sheet-primary'
                            onClick={() => setStep('finish')}
                        >{'Next'}</button>
                    </div>
                </>
            ) : null}

            {step === 'finish' ? (
                <FinishStep
                    draft={draft}
                    editing={Boolean(editing)}
                    focusName={Boolean(editing) && from !== 'emoji'}
                    working={working}
                    nameMax={limits?.name || SOUND_NAME_MAX}
                    onName={(name) => setDraft((current) => ({...current, name}))}
                    onEmoji={() => setStep('emoji')}
                    onSubmit={submit}
                />
            ) : null}

            {step === 'emoji' ? (
                <EmojiChooser
                    chosen={draft.emoji}
                    onChoose={(emoji) => {
                        setDraft((current) => ({...current, emoji}));
                        setStep('finish');
                    }}
                />
            ) : null}

            {working !== '' && step !== 'finish' ? <p className='vntv-snd-hint'>{working}</p> : null}
            {problem !== '' ? (
                <p
                    className='vntv-hint vntv-notice-error'
                    role='alert'
                >{problem}</p>
            ) : null}
        </div>
    );
};

// The microphone. The warning first, in plain words, because the desktop
// app's prompt is shared with Calls (M15) and its "Deny Permanently" would take
// the microphone from Calls too; then one big button.
const RecordStep: React.FC<{state: RecordState; onRecord: () => void; onStop: () => void}> = ({state, onRecord, onStop}) => {
    const name = useName();
    const live = state.phase === 'recording';
    const share = Math.min(1, state.ms / RECORD_MS);

    return (
        <div className='vntv-snd-record'>
            {state.phase === 'refused' ? (
                <p className='vntv-snd-warn vntv-snd-warn-refused'>
                    <span
                        className='vntv-snd-warn-icon'
                        aria-hidden={true}
                    ><InfoIcon size={15}/></span>
                    <span>{`${name} wasn’t allowed the microphone. Allow it for this server in the desktop app’s settings or the browser’s, or upload a file instead.`}</span>
                </p>
            ) : (
                <p className='vntv-snd-warn'>
                    <span
                        className='vntv-snd-warn-icon'
                        aria-hidden={true}
                    ><InfoIcon size={15}/></span>
                    <span>
                        {inDesktopApp() ?
                            'Mattermost will ask to use your microphone. Choose Allow. Don’t choose “Deny Permanently”: that would block your microphone in Calls too.' :
                            'Your browser will ask to use your microphone. Choose Allow.'}
                    </span>
                </p>
            )}

            <div className='vntv-snd-rec'>
                <button
                    type='button'
                    className='vntv-snd-rec-button'
                    data-live={live ? 'true' : undefined}
                    aria-label={live ? 'Stop recording' : 'Record'}
                    aria-disabled={state.phase === 'asking' ? true : undefined}
                    onClick={() => {
                        if (live) {
                            onStop();
                        } else if (state.phase !== 'asking') {
                            onRecord();
                        }
                    }}
                >
                    {live ? (
                        <svg
                            className='vntv-snd-rec-ring'
                            viewBox='0 0 100 100'
                            aria-hidden={true}
                        >
                            <circle
                                cx='50'
                                cy='50'
                                r='47'
                                pathLength={100}
                                strokeDasharray={`${share * 100} 100`}
                            />
                        </svg>
                    ) : null}
                    {live ? <StopIcon size={20}/> : <MicIcon size={22}/>}
                </button>
                <div className='vntv-snd-rec-side'>
                    <span className='vntv-snd-rec-title'>{live ? 'Recording' : 'Press to record'}</span>
                    <span className='vntv-snd-rec-time'>{live ? `${seconds(state.ms)} of 6.0 s` : 'Stops by itself at 6 seconds'}</span>
                </div>
            </div>

            <div
                className='vntv-snd-meter'
                aria-hidden={true}
            >
                <div
                    className='vntv-snd-wave'
                    style={{width: `${share * 100}%`}}
                >
                    {state.levels.map((level, index) => (
                        <i
                            // A level a frame: they never move.
                            // eslint-disable-next-line react/no-array-index-key
                            key={index}
                            style={{height: `${Math.max(8, level * 100)}%`}}
                        />
                    ))}
                </div>
            </div>
        </div>
    );
};

// A clip as the Clips sheet shows one, in a line: its picture (or its
// streamer's, blurred, as ClipPicture falls back), its length on it, the title
// it is known by everywhere (shownTitleOf: "Dave, Oct 12 22:05" for one with
// none), and from whose stream how long ago.
const ClipLine: React.FC<{clip: Clip}> = ({clip}) => {
    const shown = shownTitleOf(clip);

    return (
        <>
            <span className='vntv-snd-clip-thumb'>
                <ClipPicture clip={clip}/>
                <span className='vntv-snd-clip-len'>{clipTime(clip.durationMs)}</span>
            </span>
            <span className='vntv-snd-clip-words'>
                <span
                    className='vntv-snd-clip-title'
                    data-untitled={clip.title === '' ? 'true' : undefined}
                    title={shown}
                >{shown}</span>
                <span className='vntv-snd-clip-who'>{`${streamLabel(clip.streamer)} · ${agoWords(clip.createdAt, clockNow())}`}</span>
            </span>
        </>
    );
};

// The clip being cut from, over the trim: the same line, small, not a button.
const ClipRow: React.FC<{clip: Clip}> = ({clip}) => (
    <div className='vntv-snd-clip vntv-snd-clip-chosen'>
        <ClipLine clip={clip}/>
    </div>
);

// Past this many clips, a search over them.
const CLIPS_BEFORE_SEARCH = 6;

// Every clip the reader may watch (GET /clips, which answers only those),
// newest first, in a list that scrolls inside the sheet; past six of them a
// search by title or streamer over it. Nothing is cut off: a clip of last
// month is as reachable as tonight's.
const ClipPicker: React.FC<{clips: Clip[] | null; onPick: (clip: Clip) => void}> = ({clips, onPick}) => {
    const [query, setQuery] = React.useState('');
    const below = useMoreBelow();
    const words = query.trim().toLowerCase();
    const shown = React.useMemo(() => (clips || []).
        slice().
        sort((a, b) => b.createdAt - a.createdAt).
        filter((clip) => words === '' || shownTitleOf(clip).toLowerCase().includes(words) || clip.streamer.name.toLowerCase().includes(words)),
    [clips, words]);

    if (clips === null) {
        return <p className='vntv-snd-hint'>{'Loading clips…'}</p>;
    }
    if (clips.length === 0) {
        return <p className='vntv-snd-hint'>{'There are no clips to cut from just now.'}</p>;
    }

    return (
        <div className='vntv-snd-clip-pick'>
            {clips.length > CLIPS_BEFORE_SEARCH ? (
                <input
                    className='vntv-input'
                    type='search'
                    value={query}
                    placeholder={`Search ${clips.length} clips`}
                    aria-label='Search clips'
                    autoComplete='off'
                    spellCheck={false}
                    onChange={(event) => setQuery(event.target.value)}
                />
            ) : null}
            {shown.length === 0 ? (
                <p className='vntv-snd-hint'>{`No clip called “${query.trim()}”.`}</p>
            ) : (
                <div
                    ref={below.ref}
                    className='vntv-snd-scroll vntv-snd-clips-scroll'
                    data-more={below.more ? 'true' : undefined}
                    onScroll={below.onScroll}
                >
                    <ul className='vntv-snd-clips'>
                        {shown.map((clip) => (
                            <li key={clip.id}>
                                <button
                                    type='button'
                                    className='vntv-snd-clip'
                                    aria-label={`Cut from ${shownTitleOf(clip)}`}
                                    onClick={() => onPick(clip)}
                                >
                                    <ClipLine clip={clip}/>
                                </button>
                            </li>
                        ))}
                    </ul>
                </div>
            )}
        </div>
    );
};

const percent = (ms: number, durationMs: number): string => `${durationMs > 0 ? Math.min(100, Math.max(0, (ms / durationMs) * 100)) : 0}%`;

// A long sound (a clip can be minutes long) is trimmed through a window of
// twenty seconds, so six seconds are never a sliver: the strip over the trim
// is the whole sound, the window on it what the trim shows, and a press on the
// strip moves the window and the selection there.
const VIEW_MS = 20000;
const BARS = 80;

// The loudest moment in each of BARS slices of the view.
function barsOf(decoded: Decoded, viewStart: number, viewMs: number): number[] {
    const per = decoded.durationMs / Math.max(1, decoded.peaks.length);

    return Array.from({length: BARS}, (_, bar) => {
        const from = Math.floor((viewStart + ((bar / BARS) * viewMs)) / per);
        const to = Math.max(from + 1, Math.ceil((viewStart + (((bar + 1) / BARS) * viewMs)) / per));

        return decoded.peaks.slice(from, to).reduce((most, peak) => Math.max(most, peak), 0);
    });
}

// A place in a sound or a clip: "0:12.6", or "1:03" past a minute.
function clock(ms: number): string {
    if (ms < 60000) {
        return `0:${(ms / 1000).toFixed(1).padStart(4, '0')}`;
    }
    const whole = Math.floor(ms / 1000);

    return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, '0')}`;
}

// The trim: the clip trimmer's look (red handles, the rest dimmed, a white
// playhead while listening) over the sound's own shape. Handles are sliders:
// arrows move 10 ms, Shift or Page Up and Page Down 100 ms. Listen plays the
// stretch kept as it will sound on the board, levelled, in this browser only.
const SoundTrim: React.FC<{decoded: Decoded; inMs: number; outMs: number; onChange: (inMs: number, outMs: number) => void}> = ({decoded, inMs, outMs, onChange}) => {
    const track = React.useRef<HTMLDivElement | null>(null);
    const dragging = React.useRef<'in' | 'out' | null>(null);
    const playing = React.useRef<Playing | null>(null);
    const [listening, setListening] = React.useState<number | null>(null);
    const [, setFrame] = React.useState(0);
    const durationMs = decoded.durationMs;
    const zoomed = durationMs > VIEW_MS;
    const viewMs = zoomed ? VIEW_MS : durationMs;
    const centred = (from: number, to: number) => Math.min(Math.max(0, ((from + to) / 2) - (viewMs / 2)), durationMs - viewMs);
    const [viewStart, setViewStart] = React.useState(() => (zoomed ? centred(inMs, outMs) : 0));

    const stop = React.useCallback(() => {
        playing.current?.stop();
        playing.current = null;
        setListening(null);
    }, []);

    // Listening stops when the stretch changes, and with the step.
    React.useEffect(() => stop, [stop, inMs, outMs]);

    React.useEffect(() => {
        if (listening === null) {
            return undefined;
        }

        let frame = 0;
        const tick = () => {
            setFrame((value) => value + 1);
            frame = window.requestAnimationFrame(tick);
        };
        frame = window.requestAnimationFrame(tick);

        return () => window.cancelAnimationFrame(frame);
    }, [listening]);

    const listen = () => {
        if (listening !== null) {
            stop();

            return;
        }

        playing.current = listenHere(decoded, inMs, outMs, () => {
            playing.current = null;
            setListening(null);
        });
        setListening(performance.now());
    };

    const at = (clientX: number): number => {
        const box = track.current?.getBoundingClientRect();
        if (!box || box.width === 0) {
            return viewStart;
        }

        return viewStart + Math.min(viewMs, Math.max(0, ((clientX - box.left) / box.width) * viewMs));
    };

    // Inside the window: a handle stops at its edge, and the window's edges
    // bound the trim as the sound's own do, so it is never cut below a
    // quarter of a second there either.
    const move = (edge: 'in' | 'out', ms: number) => {
        const held = Math.min(viewStart + viewMs, Math.max(viewStart, ms));
        const next = clampTrim((edge === 'in' ? held : inMs) - viewStart, (edge === 'out' ? held : outMs) - viewStart, viewMs, edge);
        onChange(viewStart + next.inMs, viewStart + next.outMs);
    };

    const keys = (edge: 'in' | 'out') => (event: React.KeyboardEvent<HTMLSpanElement>) => {
        const big = event.shiftKey || event.key === 'PageUp' || event.key === 'PageDown';
        const step = big ? 100 : 10;
        const value = edge === 'in' ? inMs : outMs;
        const by: Record<string, number> = {ArrowRight: step, ArrowUp: step, PageUp: step, ArrowLeft: -step, ArrowDown: -step, PageDown: -step};
        if (!(event.key in by)) {
            return;
        }

        event.preventDefault();
        event.stopPropagation();
        move(edge, value + by[event.key]);
    };

    const inView = (ms: number) => percent(ms - viewStart, viewMs);

    const thumb = (edge: 'in' | 'out') => (
        <span
            className={`vntv-trim-thumb ${edge === 'in' ? 'vntv-trim-thumb-start' : 'vntv-trim-thumb-end'}`}
            role='slider'
            tabIndex={0}
            aria-label={edge === 'in' ? 'Start' : 'End'}
            aria-orientation='horizontal'
            aria-valuemin={0}
            aria-valuemax={Math.round(durationMs)}
            aria-valuenow={Math.round(edge === 'in' ? inMs : outMs)}
            aria-valuetext={seconds(edge === 'in' ? inMs : outMs)}
            style={{left: inView(edge === 'in' ? inMs : outMs)}}
            onKeyDown={keys(edge)}
            onPointerDown={(event) => {
                event.preventDefault();
                event.currentTarget.setPointerCapture(event.pointerId);
                dragging.current = edge;
            }}
            onPointerMove={(event) => {
                if (dragging.current === edge) {
                    move(edge, at(event.clientX));
                }
            }}
            onPointerUp={() => {
                dragging.current = null;
            }}
        />
    );

    // A press on the strip: the window, and the selection with it, centred
    // there.
    const jumpTo = (event: React.PointerEvent<HTMLDivElement>) => {
        const box = event.currentTarget.getBoundingClientRect();
        const ms = ((event.clientX - box.left) / Math.max(1, box.width)) * durationMs;
        const length = outMs - inMs;
        const start = Math.min(Math.max(0, ms - (length / 2)), durationMs - length);
        setViewStart(centred(start, start + length));
        onChange(start, start + length);
    };

    const position = listening === null ? null : Math.min(outMs, inMs + (performance.now() - listening));

    return (
        <div className='vntv-trim vntv-snd-trim'>
            {zoomed ? (
                <div
                    className='vntv-snd-overview'
                    aria-hidden={true}
                    onPointerDown={jumpTo}
                >
                    <span
                        className='vntv-snd-overview-view'
                        style={{left: percent(viewStart, durationMs), width: percent(viewMs, durationMs)}}
                    />
                    <span
                        className='vntv-snd-overview-sel'
                        style={{left: percent(inMs, durationMs), width: percent(outMs - inMs, durationMs)}}
                    />
                </div>
            ) : null}
            <div
                ref={track}
                className='vntv-trim-track'
            >
                <div
                    className='vntv-snd-wave'
                    aria-hidden={true}
                >
                    {barsOf(decoded, viewStart, viewMs).map((peak, index) => (
                        <i
                            // A slice of the window each: they never move.
                            // eslint-disable-next-line react/no-array-index-key
                            key={index}
                            style={{height: `${Math.max(6, peak * 100)}%`}}
                        />
                    ))}
                </div>
                <span
                    className='vntv-trim-dim'
                    style={{left: 0, width: inView(inMs)}}
                />
                <span
                    className='vntv-trim-sel'
                    style={{left: inView(inMs), width: percent(outMs - inMs, viewMs)}}
                />
                <span
                    className='vntv-trim-dim'
                    style={{left: inView(outMs), right: 0}}
                />
                {position === null ? null : (
                    <span
                        className='vntv-trim-thumb vntv-trim-head'
                        aria-hidden={true}
                        style={{left: inView(position)}}
                    />
                )}
                {thumb('in')}
                {thumb('out')}
            </div>
            <div className='vntv-snd-readout'>
                <button
                    type='button'
                    className='vntv-sheet-action vntv-snd-listen'
                    onClick={listen}
                >
                    {listening === null ? <PlayIcon size={12}/> : <PauseIcon size={12}/>}
                    {listening === null ? 'Listen' : 'Stop'}
                </button>
                <span className='vntv-trim-readout'>{seconds(outMs - inMs)}</span>
                <span className='vntv-snd-from'>{`from ${clock(inMs)}`}</span>
            </div>
        </div>
    );
};

// The last step: the tile as it will be on the board, its emoji and its name.
const FinishStep: React.FC<{
    draft: Draft;
    editing: boolean;

    // Rename: the name field takes the focus as the step opens.
    focusName: boolean;
    working: string;
    nameMax: number;
    onName: (name: string) => void;
    onEmoji: () => void;
    onSubmit: () => void;
}> = ({draft, editing, focusName, working, nameMax, onName, onEmoji, onSubmit}) => {
    const left = nameLeft(draft.name);
    const shown = draft.name.trim() || 'Airhorn';
    const field = React.useRef<HTMLInputElement | null>(null);

    React.useEffect(() => {
        if (focusName) {
            field.current?.select();
        }
    }, [focusName]);

    return (
        <div className='vntv-snd-finish'>
            <div className='vntv-snd-made'>
                <div
                    className='vntv-snd-grid vntv-snd-sample'
                    data-phase='ready'
                    aria-hidden={true}
                >
                    <div className='vntv-snd-cell'>
                        <span
                            className='vntv-snd-tile'
                            data-empty={draft.name.trim() === '' ? 'true' : undefined}
                        >
                            <span className='vntv-snd-face'>
                                <SoundEmojiImage emoji={draft.emoji}/>
                                <span className='vntv-snd-name'>{shown}</span>
                            </span>
                        </span>
                    </div>
                </div>
                <div className='vntv-snd-fields'>
                    <button
                        type='button'
                        className='vntv-snd-emoji-button'
                        aria-label={`Emoji: ${draft.emoji.name}. Change it`}
                        onClick={onEmoji}
                    >
                        <SoundEmojiImage
                            emoji={draft.emoji}
                            className='vntv-snd-emoji-small'
                        />
                        <span className='vntv-snd-emoji-name'>{`:${draft.emoji.name}:`}</span>
                        <span className='vntv-snd-emoji-change'>{'Change'}</span>
                    </button>
                    <label className='vntv-snd-name-field'>
                        <span className='vntv-sr-only'>{'Name'}</span>
                        <input
                            ref={field}
                            className='vntv-input'
                            value={draft.name}
                            maxLength={nameMax}
                            placeholder='Airhorn'
                            autoComplete='off'
                            spellCheck={false}
                            onChange={(event) => onName(event.target.value.replace(/[\r\n]/g, ''))}
                            onKeyDown={(event) => {
                                if (event.key === 'Enter') {
                                    event.preventDefault();
                                    onSubmit();
                                }
                            }}
                        />
                        <span
                            className='vntv-snd-left'
                            data-low={left <= 3 ? 'true' : undefined}
                        >{`${left} left`}</span>
                    </label>
                </div>
            </div>
            <button
                type='button'
                className='vntv-sheet-primary vntv-snd-submit'
                aria-disabled={working !== '' ? true : undefined}
                onClick={onSubmit}
            >{working || (editing ? 'Save' : 'Add to the board')}</button>
        </div>
    );
};
