import React from 'react';

import {fetchSoundAudio, fetchSounds, playSound, removeMySounds, removeSound, SoundRequestError} from '../client';
import {now as clockNow, onClockAdvance} from '../clock';
import {composing} from '../keys';
import {nameNow, useName} from '../name';
import {askOwn} from '../ownAnswers';
import {canHearSounds, canMakeSounds, playStored} from '../soundEncode';
import type {Playing} from '../soundEncode';
import {getSoundBusy, onSoundBusy} from '../soundboard';
import {BOARD_FULL, YOURS_FULL, boardPhase, canAddWhile, emptyBoard, nextIndex, noteOf, refusalLine, tileTitle, whyLine} from '../soundLogic';
import type {BoardPhase, BusyHeard, OwnPlay} from '../soundLogic';
import {useTwoClicks} from '../twoClicks';
import type {Sound, SoundBoard} from '../types';

import {AddSound, SoundSources, UNSUPPORTED} from './AddSound';
import type {AddFrom} from './AddSound';
import {SoundEmojiImage} from './EmojiChooser';
import {BinIcon, MoreIcon, PencilIcon, PlayIcon, PlusIcon, SmileIcon, SoundboardIcon, UnpluggedIcon} from './icons';
import {SheetHead} from './SheetHead';

// The soundboard (design/3.2.0.md 7.13, with Victor's P1 picks): its button,
// the sixth tool in the footer's bar, shown only while the reader is in voice,
// and its sheet, which rises from the bar like every other sheet, one at a
// time, with square'ish tiles: the emoji over the name, inside the tile.
//
// Everything the sheet shows comes from GET /sounds, asked when it opens,
// when the board's version in the snapshot moves, and when the reader's own
// row in the voice roster changes (muted, unmuted, gone); from the answer to a
// press; and from the sound event, which says until when somebody's sound
// plays (soundboard.ts). The tiles' state at any moment is soundLogic.ts's
// boardPhase, and the sheet ticks only while something counts down. The
// server holds every rule; the grey tiles are a courtesy (7.15).

// The first time ever in a browser, the button carries the small red dot
// until it is opened. Per browser, and wrapped: storage can throw.
const SEEN_KEY = 'villanet.soundboardSeen';

function seenBefore(): boolean {
    try {
        return window.localStorage.getItem(SEEN_KEY) === '1';
    } catch {
        return true;
    }
}

function markSeen(): void {
    try {
        window.localStorage.setItem(SEEN_KEY, '1');
    } catch {
        // Nothing to remember it in; the dot goes when the panel does.
    }
}

type ButtonProps = {
    open: boolean;
    muted: boolean;
    sheetId: string;
    onToggle: () => void;
    buttonRef?: React.MutableRefObject<HTMLButtonElement | null>;
};

// The button: a speaker with its waves, a slash across it while the reader is
// muted (it stays, so they can open it and read why). The first tool in the
// bar, at 17 px in the bar's 34 px square.
export const SoundboardTool: React.FC<ButtonProps> = ({open, muted, sheetId, onToggle, buttonRef}) => {
    const [fresh, setFresh] = React.useState(() => !seenBefore());

    React.useEffect(() => {
        if (open && fresh) {
            markSeen();
            setFresh(false);
        }
    }, [open, fresh]);

    return (
        <button
            ref={buttonRef}
            type='button'
            className='vntv-bar-tool vntv-bar-sounds'
            data-fresh={fresh ? 'true' : undefined}
            aria-label='Soundboard'
            aria-expanded={open}
            aria-controls={open ? sheetId : undefined}
            title={muted ? 'Soundboard: off while you’re muted in Mumble' : 'Soundboard: play a sound into your Mumble channel'}
            onClick={onToggle}
        >
            <SoundboardIcon
                size={17}
                off={muted}
            />
            {fresh ? (
                <span
                    className='vntv-snd-dot'
                    aria-hidden={true}
                />
            ) : null}
        </button>
    );
};

// A refusal's words (7.14), for the line under the grid.
function refusalWords(reason: unknown, channel?: string): string {
    if (reason instanceof SoundRequestError) {
        return refusalLine(reason.code, nameNow(), reason.retryAfterMs, channel);
    }

    return refusalLine('store', nameNow());
}

type TileProps = {
    sound: Sound;
    phase: BoardPhase;
    tabbable: boolean;
    fresh: boolean;
    menuOpen: boolean;
    onPress: (sound: Sound) => void;
    onKey: (event: React.KeyboardEvent<HTMLButtonElement>) => void;
    onFocus: () => void;
    onMenu: (sound: Sound, byKey: boolean) => void;
};

// One sound: the tile, the emoji in its upper part and the name under it, on
// one line or two. While your own sound plays a red ring runs once round the
// tile's edge; through your wait the tile you pressed is swept and counts its
// seconds down in its corner while the others dim.
//
// Your own sounds' ⋯ is for pointing; from the keyboard the same choices are
// a focused tile's context menu (Shift+F10, or the menu key), and a right
// click opens them too. The ⋯ stays out of the tab order, so the grid keeps
// one tab stop.
const SoundTile: React.FC<TileProps> = ({sound, phase, tabbable, fresh, menuOpen, onPress, onKey, onFocus, onMenu}) => {
    const inert = phase.kind !== 'ready';
    const playing = phase.kind === 'playing' && phase.id === sound.id;
    const pressed = phase.kind === 'cooldown' && phase.id === sound.id;
    const progress = {
        '--vntv-snd-left': phase.kind === 'cooldown' ? phase.left : 0,
    } as React.CSSProperties;

    return (
        <div
            className='vntv-snd-cell'
            data-active={playing || pressed ? 'true' : undefined}
            data-mine={sound.mine ? 'true' : undefined}
            data-menu={menuOpen ? 'true' : undefined}
        >
            <button
                type='button'
                className='vntv-snd-tile'
                data-playing={playing ? 'true' : undefined}
                data-pressed={pressed ? 'true' : undefined}
                data-fresh={fresh ? 'true' : undefined}
                tabIndex={tabbable ? 0 : -1}
                aria-label={`Play ${sound.name}`}
                aria-disabled={inert ? true : undefined}
                aria-keyshortcuts={sound.mine ? 'Shift+F10' : undefined}
                title={tileTitle(sound)}
                style={progress}
                onClick={() => onPress(sound)}
                onKeyDown={onKey}
                onFocus={onFocus}
                onContextMenu={sound.mine ? (event) => {
                    event.preventDefault();
                    onMenu(sound, false);
                } : undefined}
            >
                <span className='vntv-snd-face'>
                    <SoundEmojiImage emoji={sound.emoji}/>
                    <span className='vntv-snd-name'>{sound.name}</span>
                    {playing ? (
                        <svg
                            className='vntv-snd-ring'
                            viewBox='0 0 100 100'
                            preserveAspectRatio='none'
                            aria-hidden={true}
                        >
                            <rect
                                x='1.5'
                                y='1.5'
                                width='97'
                                height='97'
                                pathLength={100}
                                strokeDasharray={`${phase.kind === 'playing' ? phase.done * 100 : 0} 100`}
                            />
                        </svg>
                    ) : null}
                    {pressed ? (
                        <span
                            className='vntv-snd-sweep'
                            aria-hidden={true}
                        />
                    ) : null}
                    {pressed && phase.kind === 'cooldown' ? (
                        <span
                            className='vntv-snd-count'
                            aria-hidden={true}
                        >{phase.seconds}</span>
                    ) : null}
                </span>
            </button>
            {sound.mine ? (
                <button
                    type='button'
                    className='vntv-snd-more'
                    aria-label={`More for ${sound.name}`}
                    aria-expanded={menuOpen}
                    tabIndex={-1}
                    onClick={() => onMenu(sound, false)}
                ><MoreIcon size={14}/></button>
            ) : null}
        </div>
    );
};

// The add tile: a dashed outline, a plus, ADD. Never dimmed or greyed with the
// sounds: adding one makes no sound, so it is never what is being held back.
const AddTile: React.FC<{onAdd: () => void}> = ({onAdd}) => (
    <div
        className='vntv-snd-cell'
        data-add='true'
    >
        <button
            type='button'
            className='vntv-snd-tile vntv-snd-addtile'
            tabIndex={-1}
            aria-label='Add a sound'
            title='Add a sound'
            onClick={onAdd}
        >
            <span className='vntv-snd-face'>
                <span className='vntv-snd-plus'><PlusIcon size={18}/></span>
                <span className='vntv-snd-name'>{'Add'}</span>
            </span>
        </button>
    </div>
);

type SheetProps = {
    id: string;

    // The board's version from the snapshot, and the reader's own row in the
    // voice roster: either moving asks for the board again.
    version: string;
    rosterKey: string;
    onClose: () => void;
};

// What the sheet is showing: the board, or the add flow in its place. One of
// your own sounds is changed in the add flow's last steps: Rename opens the
// name, Change emoji the chooser.
type Mode = {kind: 'board'} | {kind: 'add'; from: AddFrom; key: number} |
    {kind: 'edit'; sound: Sound; step: 'finish' | 'emoji'; key: number};

// Where one of your own sounds' choices open: in the grid, as a row of its own
// after the row that holds the sound, with a notch pointing up at it. So they
// open beside the sound even on a board long enough to scroll, and push the
// rows under them down rather than covering them.
type MenuPlace = {id: string; after: number; left: number; notch: number; byKey: boolean};

const MENU_WIDTH = 188;

// The sound event's word, as a hook: until when somebody's sound plays.
function useSoundBusy(): {until: number; heardAt: number} {
    const [busy, setBusy] = React.useState(getSoundBusy);

    React.useEffect(() => onSoundBusy(() => setBusy(getSoundBusy())), []);

    return busy;
}

// Where the focus goes when the control that had it has gone (ADD, the add
// flow, a sound's choices, Delete all my sounds): to the tile with this id
// once the board shows it, or else to whatever is at this place in the grid.
// A wish made as the board is asked again waits, on the sheet itself so the
// keyboard and Escape still work, for the answer to that asking (asked, the
// number of the request), not for one that was already on its way.
type FocusWish = {id: string; index: number; asked: number};

// The thin scrollbar's width, taken from the sheet's right padding, so the
// grid is as wide when the sheet scrolls as when it does not: a sheet that
// starts to scroll (muted, and the reason box appears) must not take a column
// away and move every tile under the reader's finger (7.7).
const SHEET_PADDING_RIGHT = 14;

export const SoundboardSheet: React.FC<SheetProps> = ({id, version, rosterKey, onClose}) => {
    const name = useName();
    const [board, setBoard] = React.useState<SoundBoard | null>(null);

    // The last list there was: kept when Mumble goes, so the tiles stay on
    // screen, greyed, with the reason, rather than the sheet emptying.
    const [sounds, setSounds] = React.useState<Sound[] | null>(null);
    const [own, setOwn] = React.useState<OwnPlay | null>(null);
    const [heard, setHeard] = React.useState<BusyHeard | null>(null);
    const [said, setSaid] = React.useState('');

    // What a screen reader is told, in one live region that is always in the
    // sheet, in board and add mode alike, so whatever is put in it is
    // announced: the status line's words, "Ready again in 10 seconds." as a
    // wait starts, and Listen here's "Playing … for you only." (7.14). Each
    // announcement is its own; nothing is said twice because something else
    // stopped being said.
    const [spoken, setSpoken] = React.useState({words: '', turn: 0});
    const announce = React.useCallback((words: string) => {
        if (words !== '') {
            setSpoken((last) => ({words, turn: last.turn + 1}));
        }
    }, []);
    const tell = React.useCallback((words: string) => {
        setSaid(words);
        announce(words);
    }, [announce]);

    const [freshId, setFreshId] = React.useState('');
    const [menu, setMenu] = React.useState<MenuPlace | null>(null);
    const [mode, setMode] = React.useState<Mode>({kind: 'board'});
    const [focusIndex, setFocusIndex] = React.useState(0);
    const [hearable, setHearable] = React.useState(false);
    const [makeable, setMakeable] = React.useState<boolean | null>(null);
    const [gutter, setGutter] = React.useState(0);
    const twice = useTwoClicks();
    const gridRef = React.useRef<HTMLDivElement | null>(null);

    // The number of the newest request for the board, and of the one whose
    // answer the sheet shows.
    const asked = React.useRef(0);
    const [answered, setAnswered] = React.useState(0);
    const alive = React.useRef(true);
    const listening = React.useRef<Playing | null>(null);
    const listenTurn = React.useRef(0);
    const focusWish = React.useRef<FocusWish | null>(null);
    const focusInside = React.useRef(false);
    const event = useSoundBusy();

    // Focus goes into the sheet as it opens, so Tab goes on to its tiles; the
    // footer gives it back to the tool when it closes.
    const sheetRef = React.useRef<HTMLDivElement | null>(null);
    React.useEffect(() => {
        sheetRef.current?.focus({preventScroll: true});
    }, []);

    // The room the scrollbar keeps (none where scrollbars float over the
    // page), measured once before the sheet is first painted.
    React.useLayoutEffect(() => {
        const sheet = sheetRef.current;
        if (sheet) {
            const style = getComputedStyle(sheet);
            const borders = (parseFloat(style.borderLeftWidth) || 0) + (parseFloat(style.borderRightWidth) || 0);
            setGutter(Math.max(0, Math.min(SHEET_PADDING_RIGHT - 4, Math.round(sheet.offsetWidth - sheet.clientWidth - borders))));
        }
    }, []);

    React.useEffect(() => {
        void canHearSounds().then((can) => alive.current && setHearable(can));
        void canMakeSounds().then((can) => alive.current && setMakeable(can));
    }, []);

    // Nothing of Listen here outlives the sheet: one still being fetched or
    // decoded is never started.
    React.useEffect(() => () => {
        alive.current = false;
        listenTurn.current += 1;
        listening.current?.stop();
        listening.current = null;
    }, []);

    // Whether the keyboard focus is the sheet's to look after: since it went
    // into the sheet, and until it went somewhere else in the page, by Tab or
    // by a press outside. A focused control that is taken away leaves focus
    // on nothing, and then it comes back into the sheet, never otherwise.
    React.useEffect(() => {
        const outside = (pointer: PointerEvent) => {
            if (!sheetRef.current?.contains(pointer.target as Node)) {
                focusInside.current = false;
            }
        };
        document.addEventListener('pointerdown', outside, true);

        return () => document.removeEventListener('pointerdown', outside, true);
    }, []);

    const load = React.useCallback(() => {
        asked.current += 1;
        const ask = asked.current;
        askOwn(fetchSounds).then((answer) => {
            if (ask !== asked.current || !answer) {
                return;
            }
            setBoard(answer);

            // Somebody in Mumble always gets the board, even an empty one; a
            // server that leaves an empty list out (3.2.0 did) must still let
            // them add the first sound rather than wait for a list forever.
            if (answer.sounds) {
                setSounds(answer.sounds);
            } else if (answer.voice?.in) {
                setSounds([]);
            }
            const at = clockNow();
            setHeard(answer.busyUntil > at ? {until: answer.busyUntil, heardAt: at} : null);

            // Last: a focus wish waiting for this answer must see the list it
            // brought, and on Mattermost's legacy root each of the calls above
            // renders, and runs the effects of the one before, on its own.
            setAnswered(ask);
        }).catch((reason: unknown) => {
            if (ask === asked.current) {
                setAnswered(ask);
                tell(refusalWords(reason));
            }
        });
    }, [tell]);

    React.useEffect(load, [load, version, rosterKey]);

    // The sound event: somebody's sound starts or ends. Ending asks for the
    // board again, so the waits are the server's.
    React.useEffect(() => {
        if (event.until === 0) {
            return;
        }
        setHeard({until: event.until, heardAt: event.heardAt});
        if (event.until <= clockNow()) {
            load();
        }
    }, [event, load]);

    // The clock, while something counts down: a frame at a time, and at once
    // when the lab winds its clock.
    const [at, setAt] = React.useState(clockNow);
    const phase = boardPhase(board, own, heard, at);
    const counting = phase.kind === 'playing' || phase.kind === 'cooldown' || phase.kind === 'busy';
    React.useEffect(() => onClockAdvance(() => setAt(clockNow())), []);
    React.useEffect(() => {
        if (!counting) {
            return undefined;
        }

        let frame = 0;
        const tick = () => {
            setAt(clockNow());
            frame = window.requestAnimationFrame(tick);
        };
        frame = window.requestAnimationFrame(tick);

        return () => window.cancelAnimationFrame(frame);
    }, [counting]);

    // Told once, for screen readers, as the wait starts.
    const waitWords = phase.kind === 'cooldown' && phase.left > 0.9 ? `Ready again in ${phase.seconds} seconds.` : '';
    React.useEffect(() => announce(waitWords), [waitWords, announce]);

    const channel = board?.voice.channel;
    const press = (sound: Sound) => {
        if (phase.kind !== 'ready') {
            return;
        }

        setSaid('');
        setMenu(null);
        playSound(sound.id).then((answer) => {
            setOwn({...answer, id: sound.id});
            setAt(clockNow());
        }).catch((reason: unknown) => {
            // A wait the panel did not know of: said, and the tiles wait for
            // it too, as the server will.
            if (reason instanceof SoundRequestError && reason.retryAfterMs > 0) {
                const now = clockNow();
                if (reason.code === 'cooldown') {
                    setBoard((current) => (current ? {...current, cooldownUntil: now + reason.retryAfterMs} : current));
                } else if (reason.code === 'busy') {
                    setHeard({until: now + reason.retryAfterMs, heardAt: now});
                }
            }
            tell(refusalWords(reason, channel));
            load();
        });
    };

    const list = sounds || [];
    const mine = list.filter((sound) => sound.mine).length;
    const limits = board?.limits;
    const yoursFull = Boolean(limits && limits.left <= 0);
    const boardFull = Boolean(limits && list.length >= limits.total);
    const loaded = sounds !== null;
    const why = phase.kind === 'off' ? phase.why : '';
    const adding = canAddWhile(why) && Boolean(board?.voice.in);
    const roomLine = adding && (yoursFull || boardFull) ? (yoursFull ? YOURS_FULL : BOARD_FULL) : '';

    const tilesNow = (): HTMLButtonElement[] => Array.from(gridRef.current?.querySelectorAll<HTMLButtonElement>('.vntv-snd-tile') || []);

    // Focus taken away with the control that had it comes back here, after
    // the render that took it: to the tile wished for, or the one in its
    // place, or the grid's own tab stop, the ADD tile, the empty board's
    // first way in, or the sheet itself.
    React.useEffect(() => {
        const sheet = sheetRef.current;
        if (!sheet) {
            return;
        }

        const active = document.activeElement;
        const lost = (active === null || active === document.body) && focusInside.current;
        const wish = focusWish.current;
        if (!lost && !(wish && active === sheet)) {
            return;
        }
        if (mode.kind !== 'board') {
            sheet.focus({preventScroll: true});

            return;
        }

        const tiles = tilesNow();
        if (wish) {
            const found = list.findIndex((sound) => sound.id === wish.id);
            if (found < 0 && answered < wish.asked) {
                sheet.focus({preventScroll: true});

                return;
            }
            focusWish.current = null;
            const index = found >= 0 ? found : Math.min(wish.index, tiles.length - 1);
            if (index >= 0 && tiles[index]) {
                setFocusIndex(index);
                tiles[index].focus({preventScroll: true});

                return;
            }
        }

        const target = gridRef.current?.querySelector<HTMLElement>('.vntv-snd-tile[tabindex="0"]') ||
            sheet.querySelector<HTMLElement>('.vntv-snd-addtile, .vntv-snd-invite .vntv-snd-source') || sheet;
        target.focus({preventScroll: true});
    });

    // The menu's place, from where the sound's tile sits: after the last tile
    // on its row, its notch under the tile's middle.
    const openMenu = (sound: Sound, byKey: boolean) => {
        if (menu?.id === sound.id) {
            setMenu(null);

            return;
        }

        const grid = gridRef.current;
        const cells = Array.from(grid?.querySelectorAll<HTMLElement>('.vntv-snd-cell') || []);
        const index = list.findIndex((entry) => entry.id === sound.id);
        const cell = cells[index];
        if (!grid || !cell) {
            return;
        }

        const box = grid.getBoundingClientRect();
        const tile = cell.getBoundingClientRect();
        let after = index;
        while (after + 1 < cells.length && Math.abs(cells[after + 1].getBoundingClientRect().top - tile.top) < 2) {
            after += 1;
        }
        const width = Math.min(MENU_WIDTH, box.width);
        const middle = (tile.left - box.left) + (tile.width / 2);
        const left = Math.min(Math.max(0, middle - (width / 2)), box.width - width);

        setSaid('');
        setMenu({id: sound.id, after, left, notch: middle - left, byKey});
    };

    // Back to the sound the menu was for, when it closes from the keyboard.
    const closeMenu = () => {
        const index = list.findIndex((entry) => entry.id === menu?.id);
        setMenu(null);
        if (index >= 0) {
            tilesNow()[index]?.focus();
        }
    };

    // The roving tab stop: Tab enters the grid once, arrows move in it, Enter
    // or Space plays, Home and End. On your own sound, Shift+F10 or the menu
    // key opens its choices.
    const onKey = (index: number) => (keyEvent: React.KeyboardEvent<HTMLButtonElement>) => {
        const sound = list[index];
        if (sound?.mine && (keyEvent.key === 'ContextMenu' || (keyEvent.key === 'F10' && keyEvent.shiftKey))) {
            keyEvent.preventDefault();
            openMenu(sound, true);

            return;
        }

        const tiles = tilesNow();
        const top = tiles[0]?.offsetTop;
        const columns = Math.max(1, tiles.filter((tile) => tile.offsetTop === top).length);
        const next = nextIndex(index, keyEvent.key, tiles.length, columns);
        if (next < 0) {
            return;
        }

        keyEvent.preventDefault();
        setFocusIndex(next);
        tiles[next]?.focus();
    };

    const startAdd = (from: AddFrom = 'source') => {
        setSaid('');
        setMenu(null);
        setMode({kind: 'add', from, key: Date.now()});
    };

    // Back from adding or changing a sound: to the sound changed, or to ADD.
    const backToBoard = (wish: FocusWish) => {
        focusWish.current = wish;
        setMode({kind: 'board'});
    };

    const deleteAll = (pressEvent: React.MouseEvent<HTMLButtonElement>) => {
        if (!twice.press('all', pressEvent)) {
            return;
        }

        removeMySounds().then(({deleted}) => {
            tell(deleted > 0 ? (deleted === 1 ? 'Deleted your sound.' : `Deleted your ${deleted} sounds.`) : 'You have no sounds on the board.');
            load();
            focusWish.current = {id: '', index: 0, asked: asked.current};
        }).catch((reason: unknown) => tell(refusalWords(reason, channel)));
    };

    const listen = (sound: Sound) => {
        listenTurn.current += 1;
        const turn = listenTurn.current;
        const ours = () => alive.current && turn === listenTurn.current;
        listening.current?.stop();
        listening.current = null;
        announce(`Playing ${sound.name} for you only.`);

        let started: Playing | null = null;
        fetchSoundAudio(sound.id).then((bytes) => (ours() ? playStored(bytes, () => {
            if (started !== null && listening.current === started) {
                listening.current = null;
            }
        }) : null)).then((playing) => {
            if (!playing) {
                return;
            }
            if (!ours()) {
                playing.stop();

                return;
            }
            started = playing;
            listening.current = playing;
        }).catch((reason: unknown) => {
            if (ours()) {
                tell(reason instanceof SoundRequestError ? refusalWords(reason, channel) : 'This browser can’t play it here.');
            }
        });
    };

    const menuSound = list.find((sound) => sound.id === menu?.id) || null;

    const escape = (keyEvent: React.KeyboardEvent) => {
        if (keyEvent.key !== 'Escape' || composing(keyEvent)) {
            return;
        }

        keyEvent.preventDefault();
        keyEvent.stopPropagation();
        if (menu) {
            closeMenu();
        } else {
            onClose();
        }
    };

    const sheetProps = {
        ref: sheetRef,
        id,
        className: 'vntv-sheet vntv-sheet-sounds',
        role: 'group',
        tabIndex: -1,
        style: {'--vntv-snd-gutter': `${gutter}px`} as React.CSSProperties,
        onKeyDown: escape,
        onFocus: () => {
            focusInside.current = true;
        },
        onBlur: (blur: React.FocusEvent) => {
            const next = blur.relatedTarget as Node | null;
            if (next !== null && !sheetRef.current?.contains(next)) {
                focusInside.current = false;
            }
        },
    };

    // Always in the sheet, whichever mode, and first in it, so React keeps
    // the one element as the mode changes and what is put in it is
    // announced. A space more or less tells two announcements of the same
    // words apart.
    const liveRegion = (
        <p
            className='vntv-sr-only'
            role='status'
        >{spoken.words === '' ? '' : `${spoken.words}${spoken.turn % 2 === 0 ? '' : '\u00a0'}`}</p>
    );

    if (mode.kind !== 'board') {
        return (
            <div
                {...sheetProps}
                aria-label={mode.kind === 'edit' ? `Change ${mode.sound.name}` : 'Add a sound'}
            >
                {liveRegion}
                <AddSound
                    key={mode.key}
                    from={mode.kind === 'add' ? mode.from : mode.step}
                    editing={mode.kind === 'edit' ? mode.sound : undefined}
                    limits={limits}
                    onBack={() => {
                        const index = mode.kind === 'edit' ? list.findIndex((sound) => sound.id === mode.sound.id) : list.length;
                        backToBoard({id: mode.kind === 'edit' ? mode.sound.id : '', index, asked: 0});
                    }}
                    onClose={onClose}
                    onAdded={(sound) => {
                        load();
                        backToBoard({id: sound.id, index: list.length, asked: mode.kind === 'edit' ? 0 : asked.current});
                        setFreshId(sound.id);
                        tell(mode.kind === 'edit' ? 'Saved.' : `Added “${sound.name}”.`);
                    }}
                />
            </div>
        );
    }

    const cells: React.ReactNode[] = list.map((sound, index) => (
        <SoundTile
            key={sound.id}
            sound={sound}
            phase={phase}
            tabbable={index === Math.min(focusIndex, list.length - 1)}
            fresh={sound.id === freshId}
            menuOpen={menu?.id === sound.id}
            onPress={press}
            onKey={onKey(index)}
            onFocus={() => setFocusIndex(index)}
            onMenu={openMenu}
        />
    ));
    if (adding && !yoursFull && !boardFull) {
        cells.push(
            <AddTile
                key='add'
                onAdd={() => startAdd()}
            />,
        );
    }
    if (menu && menuSound) {
        cells.splice(menu.after + 1, 0, (
            <SoundMenu
                key='menu'
                sound={menuSound}
                place={menu}
                hearable={hearable}
                onListen={() => {
                    closeMenu();
                    listen(menuSound);
                }}
                onEdit={(step) => {
                    setMenu(null);
                    setMode({kind: 'edit', sound: menuSound, step, key: Date.now()});
                }}
                onDeleted={() => {
                    setMenu(null);
                    tell('Deleted.');
                    load();
                    focusWish.current = {id: '', index: list.findIndex((sound) => sound.id === menuSound.id), asked: asked.current};
                }}
                onRefused={(reason) => {
                    closeMenu();
                    tell(refusalWords(reason, channel));
                    load();
                }}
            />
        ));
    }

    // The line under the head: where it plays, or the wait, or somebody
    // else's sound (noteOf); the reasons the tiles are off go in their own
    // box under it.
    const note = phase.kind === 'off' ? '' : noteOf(phase, channel);
    const barKind = phase.kind === 'busy' || phase.kind === 'cooldown' ? phase.kind : 'none';

    return (
        <div
            {...sheetProps}
            aria-label='Soundboard'
        >
            {liveRegion}
            <SheetHead
                title='Soundboard'
                note={note}
                onClose={onClose}
            />

            {/* Somebody else's sound: a thin red bar emptying over what is
                left of it, and every tile dimmed and inert until it ends.
                Your own wait: the same bar in grey. Always in the sheet,
                hidden while nothing counts down, so that a press, or a sound
                of somebody else's, never moves the sheet's top edge. */}
            <div
                className='vntv-snd-busy'
                data-kind={barKind}
                aria-hidden={true}
            >
                <span style={{transform: `scaleX(${phase.kind === 'busy' || phase.kind === 'cooldown' ? phase.left : 0})`}}/>
            </div>

            {why ? (
                <p className='vntv-snd-why'>
                    <span
                        className='vntv-snd-why-icon'
                        aria-hidden={true}
                    >
                        {why === 'bot_offline' || why === 'bot_cannot' || why === 'not_in_voice' ? <UnpluggedIcon size={14}/> : (
                            <SoundboardIcon
                                size={14}
                                off={true}
                            />
                        )}
                    </span>
                    <span>{whyLine(why, name, channel)}</span>
                </p>
            ) : null}

            {/* The empty board's invitation: its three ways in, or, where
                this browser cannot make a sound, that instead (7.13 step 1),
                before anything is recorded or read. */}
            {loaded && list.length === 0 && adding ? (
                <div className='vntv-snd-invite'>
                    <p className='vntv-snd-invite-words'>{emptyBoard(name)}</p>
                    {makeable === false ? <p className='vntv-snd-why'>{UNSUPPORTED}</p> : <SoundSources onPick={startAdd}/>}
                </div>
            ) : null}

            {list.length > 0 ? (
                <div
                    ref={gridRef}
                    className='vntv-snd-grid'
                    data-phase={phase.kind}
                    role='group'
                    aria-label='Sounds'
                >
                    {cells}
                </div>
            ) : null}

            {roomLine !== '' ? <p className='vntv-snd-hint'>{roomLine}</p> : null}

            {/* Deleting your own is a takeback (7.7): offered whenever you
                have something on the board, whatever Mumble says. The words
                beside it are said by the live region below. */}
            {said !== '' || mine > 0 ? (
                <div className='vntv-snd-foot'>
                    <span className='vntv-snd-said'>{said}</span>
                    {mine > 0 ? (
                        <button
                            type='button'
                            className='vntv-snd-quiet'
                            data-armed={twice.armed === 'all' ? 'true' : undefined}
                            onClick={deleteAll}
                        >{twice.armed === 'all' ? `Click again to delete all ${mine}` : 'Delete all my sounds'}</button>
                    ) : null}
                </div>
            ) : null}
        </div>
    );
};

type MenuProps = {
    sound: Sound;
    place: MenuPlace;

    // Listen here is offered only where the browser can decode the sound.
    hearable: boolean;
    onListen: () => void;
    onEdit: (step: 'finish' | 'emoji') => void;
    onDeleted: () => void;
    onRefused: (reason: unknown) => void;
};

// One of your own sounds' choices (7.13): Listen here, Rename, Change emoji,
// Delete (two presses). A short list under the sound's row, pointing at it.
// Opened from the keyboard, focus goes to its first choice; arrows move,
// Escape (the sheet's) closes it and goes back to the sound. A refused delete
// says its refusal's words.
const SoundMenu: React.FC<MenuProps> = ({sound, place, hearable, onListen, onEdit, onDeleted, onRefused}) => {
    const twice = useTwoClicks();
    const box = React.useRef<HTMLDivElement | null>(null);

    React.useEffect(() => {
        if (place.byKey) {
            box.current?.querySelector<HTMLButtonElement>('button')?.focus();
        }
    }, [place.byKey]);

    const remove = (event: React.MouseEvent<HTMLButtonElement>) => {
        if (twice.press('delete', event)) {
            removeSound(sound.id).then(onDeleted).catch(onRefused);
        }
    };

    const keys = (event: React.KeyboardEvent<HTMLDivElement>) => {
        const items = Array.from(box.current?.querySelectorAll<HTMLButtonElement>('button') || []);
        const at = items.indexOf(document.activeElement as HTMLButtonElement);
        const by: Record<string, number> = {ArrowDown: 1, ArrowUp: -1};
        if (!(event.key in by) || items.length === 0) {
            return;
        }

        event.preventDefault();
        items[(at + by[event.key] + items.length) % items.length]?.focus();
    };

    return (
        <div
            ref={box}
            className='vntv-snd-menu'
            role='group'
            aria-label={`More for ${sound.name}`}
            style={{
                '--vntv-snd-menu-left': `${place.left}px`,
                '--vntv-snd-notch': `${place.notch}px`,
                '--vntv-snd-menu-width': `${MENU_WIDTH}px`,
            } as React.CSSProperties}
            onKeyDown={keys}
        >
            <div className='vntv-snd-menu-box'>
                {hearable ? (
                    <button
                        type='button'
                        className='vntv-snd-menu-item'
                        onClick={onListen}
                    >
                        <PlayIcon size={13}/>
                        {'Listen here'}
                    </button>
                ) : null}
                <button
                    type='button'
                    className='vntv-snd-menu-item'
                    onClick={() => onEdit('finish')}
                >
                    <PencilIcon size={13}/>
                    {'Rename'}
                </button>
                <button
                    type='button'
                    className='vntv-snd-menu-item'
                    onClick={() => onEdit('emoji')}
                >
                    <SmileIcon size={13}/>
                    {'Change emoji'}
                </button>
                <button
                    type='button'
                    className='vntv-snd-menu-item'
                    data-danger='true'
                    data-armed={twice.armed === 'delete' ? 'true' : undefined}
                    onClick={remove}
                >
                    <BinIcon size={13}/>
                    {twice.armed === 'delete' ? 'Click again to delete' : 'Delete'}
                </button>
            </div>
        </div>
    );
};
