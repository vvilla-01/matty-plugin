import React from 'react';

import {accountsEventHeard} from '../src/accounts';
import {refreshIdentity} from '../src/identity';
import {meHeard} from '../src/me';

import {labSay} from './mattermost';
import {
    ME_ID,
    change,
    getWorld,
    labVictorHytaleOf,
    labVictorSteamOf,
    setDaveSteam,
    setPlaying,
    setVictorHytale,
    setVictorSteam,
    setVictorTwitch,
    subscribe,
    victorHytaleLink,
    victorSteamLink,
} from './world';
import type {LabOthersSteam, LabPlaying, LabVictorHytale, LabVictorSteam, World} from './world';

// The lab's 3.4.0 rows (design/3.4.0.md 8.3): Steam's and Hytale's switches
// and what can be wrong with each, Victor's own Steam (in each of Steam's
// states) and Hytale, how Connect goes, and the two events the server sends.
// Each row sets the fake server up for a state; the panel reads it as it
// would read the real one: "me" for Victor's own rows, GET /api/v1/accounts
// for everybody's statuses, both again on the events. The same on
// window.__villanetLab.v340, for the screenshots.

function useLabWorld(): World {
    const [, bump] = React.useState(0);
    React.useEffect(() => subscribe(() => bump((value) => value + 1)), []);

    return getWorld();
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

// The server's accounts event (to everybody: read GET /api/v1/accounts again,
// if you may watch) and "me" (to Victor: read "me" again), as the page
// handles them. The rows below change the world without either, as
// a server would between events; these send them.
function accountsEvent(): void {
    accountsEventHeard();
    labSay('The server sent accounts: everybody who may watch reads GET /api/v1/accounts again, a second later.');
}

function meEvent(): void {
    meHeard();
    labSay('The server sent "me" to Victor: "me" read again.');
}

// A change to the world that people's statuses follow at once, as the
// server's accounts event makes them.
function changeAndTell(apply: (current: World) => void): void {
    change(apply);
    accountsEventHeard();
}

// The clipboard, as the lab holds it: a page in a headless browser has no
// say over the real one, so Copy is answered here, and can be refused, as a
// browser that will not let a page write to it refuses (then the panel
// selects the name instead).
const labClipboard = {refused: false, text: ''};

function installLabClipboard(): void {
    const clipboard = {
        writeText: (text: string): Promise<void> => {
            if (labClipboard.refused) {
                return Promise.reject(new DOMException('Write permission denied.', 'NotAllowedError'));
            }

            labClipboard.text = text;

            return Promise.resolve();
        },
    };
    try {
        Object.defineProperty(navigator, 'clipboard', {configurable: true, value: clipboard});
    } catch {
        // A browser that will not let the lab stand in: Copy goes to the
        // real clipboard.
    }
}

installLabClipboard();

function setCopyRefused(refused: boolean): void {
    labClipboard.refused = refused;
    labSay(refused ? 'The clipboard now refuses Copy: the Hytale name is selected instead.' : 'The clipboard takes Copy again.');
}

// Victor's own connections at once, for the Disconnect line of the lock
// and the WATCH ONLY bar (design/3.4.0.md 8.3 items 7 and 8): none, Twitch
// alone, Steam alone, two, or all three. The switches are left as they are:
// a connection shows there whatever they say.
type LabConnections = 'none' | 'twitch' | 'steam' | 'twitch+steam' | 'all';

function setVictorConnections(set: LabConnections): void {
    const twitch = set === 'twitch' || set === 'twitch+steam' || set === 'all';
    const steam = set === 'steam' || set === 'twitch+steam' || set === 'all';
    const hytale = set === 'all';
    setVictorTwitch(twitch ? 'connected' : 'none');
    change((current) => {
        const {[ME_ID]: _steam, ...steamLinks} = current.steamLinks;
        const {[ME_ID]: _hytale, ...hytaleLinks} = current.hytaleLinks;
        current.steamLinks = steam ? {...steamLinks, [ME_ID]: victorSteamLink()} : steamLinks;
        current.steamStatus = steam ? {...current.steamStatus, [ME_ID]: {state: 'online', public: true}} : current.steamStatus;
        current.hytaleLinks = hytale ? {...hytaleLinks, [ME_ID]: victorHytaleLink()} : hytaleLinks;
        current.steamLost = null;
        current.hytaleLost = null;
    });
    void refreshIdentity();
    accountsEventHeard();
}

// The Playing line's check: a row the reader left at its start stays there
// when who is playing changes, so whoever just started is in view rather
// than behind the start's fade (PlayingStrip.tsx; design/3.4.0.md, Review
// log (build)). At 304 px, with Steam switched on: nobody, then one person,
// two, eight, one and eight again, and nobody ever scrolls. Every step must
// leave the row at scrollLeft 0, and so must every frame on the way, so a
// row that moved and came back is caught too. It leaves Steam on and eight
// people playing.
type PlayingStep = {playing: LabPlaying; people: number; scrollLeft: number; mostInAnyFrame: number};

const PLAYING_PEOPLE: Record<LabPlaying, number> = {nobody: 0, one: 1, two: 2, eight: 8};

// A frame, or 50 ms in a tab the browser is not drawing, which gets none.
function nextFrame(): Promise<void> {
    return new Promise((resolve) => {
        const timer = window.setTimeout(resolve, 50);
        requestAnimationFrame(() => {
            window.clearTimeout(timer);
            resolve();
        });
    });
}

function playingRow(): HTMLUListElement | null {
    return document.querySelector<HTMLUListElement>('.vntv-playing .vntv-voice-list');
}

async function playingStep(playing: LabPlaying): Promise<PlayingStep> {
    andTell(() => setPlaying(playing));
    const people = PLAYING_PEOPLE[playing];
    let most = 0;
    let settled = 0;

    // Until the panel has read the change (the accounts event is answered a
    // second later) and then ten frames more, or five seconds.
    for (let waited = 0; waited < 100 && settled < 10; waited++) {
        await nextFrame();
        const row = playingRow();
        most = Math.max(most, row ? row.scrollLeft : 0);
        if ((row ? row.children.length : 0) === people) {
            settled++;
        }
    }

    const row = playingRow();

    return {playing, people: row ? row.children.length : 0, scrollLeft: row ? row.scrollLeft : 0, mostInAnyFrame: most};
}

export async function playingStaysAtStart(setWidth: (px: number) => void): Promise<{ok: boolean; steps: PlayingStep[]}> {
    setWidth(304);
    changeAndTell((current) => {
        current.steamSwitch = true;
    });

    const steps: PlayingStep[] = [];
    for (const playing of ['nobody', 'one', 'two', 'eight', 'one', 'eight'] as LabPlaying[]) {
        steps.push(await playingStep(playing));
    }

    const ok = steps.every((step) => step.people === PLAYING_PEOPLE[step.playing] && step.scrollLeft === 0 && step.mostInAnyFrame === 0);
    labSay(ok ?
        'Playing stays at its start: one, two, eight, one and eight people at 304 px, scrollLeft 0 at every step and in every frame.' :
        `Playing did NOT stay at its start: ${steps.map((step) => `${step.playing} (${step.people} shown) at ${step.scrollLeft}, ${step.mostInAnyFrame} at most`).join('; ')}.`);

    return {ok, steps};
}

// Every takeover notice at once: Hytale's an hour ago, Steam's three days
// ago and Twitch's ten; OK shows the next, the newest first.
function threeNotices(): void {
    setVictorSteam('lost');
    setVictorHytale('lost');
    setVictorTwitch('lost');
    change((current) => {
        const day = 24 * 60 * 60 * 1000;
        current.steamLost = current.steamLost && {...current.steamLost, at: Date.now() - (3 * day)};
        current.twitchLost = current.twitchLost && {...current.twitchLost, at: Date.now() - (10 * day)};
    });
    void refreshIdentity();
}

// A change made by one of the world's own setters, then the accounts event,
// as the server sends it when anybody's status changes.
function andTell(run: () => void): void {
    run();
    accountsEventHeard();
}

const STEAM_KEY_REFUSED = 'Steam refused the Web API key. Check it in the Steam section.';
const STEAM_UNREACHABLE = 'Steam could not be reached; the last good check was 9 minutes ago.';
const STEAM_CALLBACK = 'Steam sends people back only to an https:// address, and the Site URL is http://chat.example.com.';
const HYTALE_SCOPE = 'Hytale has not granted the application the scope hytale:profile.';
const HYTALE_CALLBACK = 'Hytale only accepts an https:// Redirect URI, and the Site URL is http://chat.example.com.';

export const Rows340: React.FC<{Row: React.FC<RowProps>; onWidth: (px: number) => void}> = ({Row, onWidth}) => {
    const world = useLabWorld();

    return (
        <>
            <Row label='Steam'>
                <Chip
                    pressed={world.steamSwitch}
                    title='Show people’s Steam status. Off is the default world: nothing new anywhere'
                    onClick={() => changeAndTell((current) => {
                        current.steamSwitch = !current.steamSwitch;
                    })}
                >{'switch on'}</Chip>
                <Chip
                    pressed={!world.steamKeySet}
                    title='No Steam Web API key: nobody can connect, and an admin is told why'
                    onClick={() => changeAndTell((current) => {
                        current.steamKeySet = !current.steamKeySet;
                    })}
                >{'no key'}</Chip>
                <Chip
                    pressed={world.steamKeyProblem !== ''}
                    title='Steam answers 403 to the key: no statuses, and the problem for admins'
                    onClick={() => changeAndTell((current) => {
                        current.steamKeyProblem = current.steamKeyProblem ? '' : STEAM_KEY_REFUSED;
                    })}
                >{'key refused'}</Chip>
                <Chip
                    pressed={world.steamProblem !== ''}
                    title='Steam out of reach for over five minutes: names still shown, statuses not guessed'
                    onClick={() => changeAndTell((current) => {
                        current.steamProblem = current.steamProblem ? '' : STEAM_UNREACHABLE;
                    })}
                >{'Steam down'}</Chip>
                <Chip
                    pressed={world.steamCallbackProblem !== ''}
                    title='A Site URL on plain http: no return address, so nobody can connect'
                    onClick={() => changeAndTell((current) => {
                        current.steamCallbackProblem = current.steamCallbackProblem ? '' : STEAM_CALLBACK;
                    })}
                >{'callback problem'}</Chip>
                <Chip
                    pressed={Boolean(world.inactive['u-dave'])}
                    title='Dave’s Mattermost account deactivated: Steam is not asked about him, and he leaves the view'
                    onClick={() => changeAndTell((current) => {
                        const {'u-dave': gone, ...rest} = current.inactive;
                        current.inactive = gone ? rest : {...rest, 'u-dave': 'deactivated'};
                    })}
                >{'Dave deactivated'}</Chip>
                <Chip
                    pressed={world.steamQuarantined}
                    title='A damaged copy of the Steam connections set aside: the console card says so, and Delete all removes it'
                    onClick={() => change((current) => {
                        current.steamQuarantined = !current.steamQuarantined;
                    })}
                >{'set aside'}</Chip>
            </Row>

            <Row label='Hytale'>
                <Chip
                    pressed={world.hytaleSwitch}
                    title='Show people’s Hytale names. Off is the default world: nothing new anywhere'
                    onClick={() => changeAndTell((current) => {
                        current.hytaleSwitch = !current.hytaleSwitch;
                    })}
                >{'switch on'}</Chip>
                <Chip
                    pressed={!world.hytaleCredentials}
                    title='No Client ID or Client Secret: nobody can connect, and an admin is told why'
                    onClick={() => changeAndTell((current) => {
                        current.hytaleCredentials = !current.hytaleCredentials;
                    })}
                >{'no application'}</Chip>
                <Chip
                    pressed={world.hytaleProblem !== ''}
                    title='Hytale has not granted hytale:profile: the problem for admins'
                    onClick={() => changeAndTell((current) => {
                        current.hytaleProblem = current.hytaleProblem ? '' : HYTALE_SCOPE;
                    })}
                >{'scope not granted'}</Chip>
                <Chip
                    pressed={world.hytaleCallbackProblem !== ''}
                    title='A Site URL on plain http: no Redirect URI, so nobody can connect'
                    onClick={() => changeAndTell((current) => {
                        current.hytaleCallbackProblem = current.hytaleCallbackProblem ? '' : HYTALE_CALLBACK;
                    })}
                >{'callback problem'}</Chip>
                <Chip
                    pressed={world.hytaleQuarantined}
                    title='A damaged copy of the Hytale connections set aside: the console card says so, and Delete all removes it'
                    onClick={() => change((current) => {
                        current.hytaleQuarantined = !current.hytaleQuarantined;
                    })}
                >{'set aside'}</Chip>
            </Row>

            <Row label='Victor’s Steam'>
                {(['none', 'online', 'playing', 'long', 'offline', 'private', 'lost', 'blocked'] as LabVictorSteam[]).map((mode) => (
                    <Chip
                        key={mode}
                        pressed={labVictorSteamOf(world) === mode}
                        title={{
                            none: 'Not connected: his Steam row offers Connect',
                            online: 'Connected as VictorPlays, online',
                            playing: 'Connected, playing ELDEN RING',
                            long: 'Connected, playing a game with a 32-character name',
                            offline: 'Connected, offline',
                            private: 'Connected, his profile private: everybody else sees him offline, and his row says why',
                            lost: 'His account was proved by somebody else: the takeover notice',
                            blocked: 'An admin blocked him: nothing about Steam shows him',
                        }[mode]}
                        onClick={() => {
                            setVictorSteam(mode);
                            accountsEventHeard();
                        }}
                    >{mode === 'long' ? 'long game' : mode}</Chip>
                ))}
            </Row>

            <Row label='Victor’s Hytale'>
                {(['none', 'connected', 'lost', 'blocked'] as LabVictorHytale[]).map((mode) => (
                    <Chip
                        key={mode}
                        pressed={labVictorHytaleOf(world) === mode}
                        title={{
                            none: 'Not connected: his Hytale row offers Connect',
                            connected: 'Connected as VictorTheBuilder, a 16-character username',
                            lost: 'His account was proved by somebody else: the takeover notice',
                            blocked: 'An admin blocked him: nothing about Hytale shows him',
                        }[mode]}
                        onClick={() => {
                            setVictorHytale(mode);
                            accountsEventHeard();
                        }}
                    >{mode}</Chip>
                ))}
            </Row>

            <Row label='Connect accounts'>
                {(['ok', 'rate_limited', 'store', 'interactive_only'] as const).map((answer) => (
                    <Chip
                        key={answer}
                        pressed={world.accountTicket === answer}
                        title={answer === 'ok' ? 'The ticket is given' : `The ticket refused with ${answer}: the server’s sentence in place of the buttons`}
                        onClick={() => change((current) => {
                            current.accountTicket = answer;
                        })}
                    >{answer === 'ok' ? 'ticket ok' : answer}</Chip>
                ))}
                {(['opens', 'blocked', 'desktop'] as const).map((mode) => (
                    <Chip
                        key={mode}
                        pressed={world.accountPopup === mode}
                        title={{
                            opens: 'A browser window opens and takes focus',
                            blocked: 'A pop-up blocker: nothing opens, and after four seconds Waiting says so',
                            desktop: 'The desktop app: its own window opens, window.open returns null, and there is no hint once it takes focus',
                        }[mode]}
                        onClick={() => change((current) => {
                            current.accountPopup = mode;
                        })}
                    >{mode === 'desktop' ? 'desktop app' : `pop-up ${mode}`}</Chip>
                ))}
                {(['connects', 'never'] as const).map((mode) => (
                    <Chip
                        key={mode}
                        pressed={world.accountWindow === mode}
                        title={mode === 'connects' ? 'Steam or Hytale connects Victor 1.5 s later, and the server sends "me"' : 'The window never answers: Waiting waits, and gives its hints'}
                        onClick={() => change((current) => {
                            current.accountWindow = mode;
                        })}
                    >{mode === 'never' ? 'never answers' : mode}</Chip>
                ))}
                <Chip
                    pressed={world.accountUnlinkFails}
                    title='The store refuses Disconnect’s write: the row says it could not'
                    onClick={() => change((current) => {
                        current.accountUnlinkFails = !current.accountUnlinkFails;
                    })}
                >{'Disconnect fails'}</Chip>
            </Row>

            <Row label='Others on Steam'>
                {(['elden', 'thirty', 'sixty', 'nameless', 'online', 'offline'] as LabOthersSteam[]).map((mode) => (
                    <Chip
                        key={mode}
                        title={{
                            elden: 'Dave in ELDEN RING: his popover row has the game as its title, "On Steam · dave_racer" under it',
                            thirty: 'Dave in a game with a 30-character name, cut in the popover with the whole name in its title',
                            sixty: 'Dave busy in a game with a 60-character name',
                            nameless: 'Dave in a game Steam does not name: "In a game"',
                            online: 'Dave online, not in a game: Online, and not on the Playing line',
                            offline: 'Dave offline: his row faded, the word Steam not',
                        }[mode]}
                        onClick={() => andTell(() => setDaveSteam(mode))}
                    >{`Dave: ${{elden: 'ELDEN RING', thirty: '30-char game', sixty: '60-char game', nameless: 'nameless game', online: 'online', offline: 'offline'}[mode]}`}</Chip>
                ))}
                {(['nobody', 'one', 'two', 'eight'] as LabPlaying[]).map((mode) => (
                    <Chip
                        key={mode}
                        title={{
                            nobody: 'Nobody in a game: no Playing line at all',
                            one: 'Dave in ELDEN RING, the others online',
                            two: 'Dave in ELDEN RING and Bob in Hades',
                            eight: 'Eight people in a game, three of them people Mattermost has not loaded: the line scrolls sideways',
                        }[mode]}
                        onClick={() => andTell(() => setPlaying(mode))}
                    >{`${mode} playing`}</Chip>
                ))}
                <Chip
                    title='Check that a change of who is playing leaves the line at its start, so whoever just started shows: one, two and eight people at 304 px (leaves eight playing)'
                    onClick={() => {
                        void playingStaysAtStart(onWidth);
                    }}
                >{'check: stays at start'}</Chip>
                <Chip
                    title='The clipboard refuses Copy: the Hytale name is selected instead'
                    onClick={() => setCopyRefused(!labClipboard.refused)}
                >{'Copy refused'}</Chip>
            </Row>

            <Row label='Victor’s connections'>
                {(['none', 'twitch', 'steam', 'twitch+steam', 'all'] as LabConnections[]).map((set) => (
                    <Chip
                        key={set}
                        title='For the Disconnect line on the lock and over WATCH ONLY: which of Twitch, Steam and Hytale Victor has connected'
                        onClick={() => setVictorConnections(set)}
                    >{set === 'all' ? 'all three' : set}</Chip>
                ))}
                <Chip
                    title='Hytale’s, Steam’s and Twitch’s takeover notices at once, over the linked bar: the newest shows, OK shows the next'
                    onClick={threeNotices}
                >{'three notices'}</Chip>
            </Row>

            <Row label='Accounts events'>
                <Chip
                    title='accounts, to everybody: read GET /api/v1/accounts again, if you may watch'
                    onClick={accountsEvent}
                >{'accounts'}</Chip>
                <Chip
                    title='me, to Victor: read "me" again'
                    onClick={meEvent}
                >{'me'}</Chip>
            </Row>
        </>
    );
};

// For scripts (window.__villanetLab.v340): the same as the rows.
export const labHooks340 = {
    setVictorSteam: (mode: LabVictorSteam) => {
        setVictorSteam(mode);
        accountsEventHeard();
    },
    setVictorHytale: (mode: LabVictorHytale) => {
        setVictorHytale(mode);
        accountsEventHeard();
    },
    accountsEvent,
    meEvent,
    changeAndTell,
    setDaveSteam: (mode: LabOthersSteam) => andTell(() => setDaveSteam(mode)),
    setPlaying: (mode: LabPlaying) => andTell(() => setPlaying(mode)),
    setVictorConnections,
    threeNotices,
    copyRefused: setCopyRefused,
    copied: () => labClipboard.text,
};
