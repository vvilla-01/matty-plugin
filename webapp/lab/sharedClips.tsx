import React from 'react';

import {CLIP_POST_TYPE} from '../src/clipLogic';
import {dropClipCaches, openClips} from '../src/clips';
import {ClipLinkRoute} from '../src/components/ClipLinkRoute';
import {ClipPostCard} from '../src/components/ClipPostCard';
import {PlanPostCard} from '../src/components/PlanPostCard';
import {PLAN_POST_TYPE} from '../src/planLogic';
import type {MattermostChannel} from '../src/types';
import {PLUGIN_ID} from './mocks/manifest';

import {clearLabReplaces, labDispatch, labSay, useLabReplaces, useLabState} from './mattermost';
import {LAB_CHANNELS, LAB_TEAM, ME_ID, change, getWorld, labShareMessage, labShareProps, subscribe} from './world';
import type {LabClip, World} from './world';

// The lab's side of sharing (addendum 4.12): shared clips' posts as
// Mattermost draws them, in either of its two stock themes; the Share
// button's answers and the channels it can be pointed at; and a clip link
// opened signed in or signed out.
//
// Everything Mattermost would do is stood in for here and said, never done:
// a permalink preview that would navigate says so, and the addresses a clip
// link's page sends Mattermost to are listed.

// ---- Mattermost's centre channel, in Denim or Onyx ----

export type CentreTheme = 'denim' | 'onyx';

// The variables Mattermost sets for every theme that the card reads, with
// their -rgb twins, as Denim and Onyx set them (mattermost-redux
// constants/preferences.ts at v11.11.1, lines 95-122 and 207-234:
// centerChannelBg, centerChannelColor, buttonBg, buttonColor, linkColor,
// errorTextColor). Onyx's were 9.0's until 3.2.0, which drew the cards on a
// darker channel than Mattermost's own.
export const THEMES: Record<CentreTheme, Record<string, string>> = {
    denim: {
        '--center-channel-bg': '#ffffff',
        '--center-channel-bg-rgb': '255, 255, 255',
        '--center-channel-color': '#3f4350',
        '--center-channel-color-rgb': '63, 67, 80',
        '--button-bg': '#1c58d9',
        '--button-bg-rgb': '28, 88, 217',
        '--button-color': '#ffffff',
        '--button-color-rgb': '255, 255, 255',
        '--link-color': '#386fe5',
        '--link-color-rgb': '56, 111, 229',
        '--error-text': '#d24b4e',
        '--error-text-color-rgb': '210, 75, 78',
    },
    onyx: {
        '--center-channel-bg': '#191b1f',
        '--center-channel-bg-rgb': '25, 27, 31',
        '--center-channel-color': '#e3e4e8',
        '--center-channel-color-rgb': '227, 228, 232',
        '--button-bg': '#4a7ce8',
        '--button-bg-rgb': '74, 124, 232',
        '--button-color': '#ffffff',
        '--button-color-rgb': '255, 255, 255',
        '--link-color': '#5d89ea',
        '--link-color-rgb': '93, 137, 234',
        '--error-text': '#da6c6e',
        '--error-text-color-rgb': '218, 108, 110',
    },
};

// How the cards are drawn: the theme, Mattermost's compact message display,
// and the width of a thread in the side panel. Outside React, so the cards in
// the lab's channel and in the Shared clips block agree.
const cardLayout = {theme: 'denim' as CentreTheme, compact: false, narrow: false};
const layoutListeners = new Set<() => void>();

function setLayout(next: Partial<typeof cardLayout>): void {
    Object.assign(cardLayout, next);
    layoutListeners.forEach((listener) => listener());
}

// The same, for the 3.2.0 rows and their script handles.
export const setCardLayout = setLayout;

export function useCardLayout(): typeof cardLayout {
    const [, bump] = React.useState(0);
    React.useEffect(() => {
        const listener = () => bump((value) => value + 1);
        layoutListeners.add(listener);

        return () => {
            layoutListeners.delete(listener);
        };
    }, []);

    return cardLayout;
}

// A piece of Mattermost's centre channel in the chosen theme: its variables,
// its background and its text colour.
export const LabCentre: React.FC<{children: React.ReactNode; narrow?: boolean}> = ({children, narrow}) => {
    const layout = useCardLayout();
    const vars = THEMES[layout.theme];
    const style = {
        ...vars,
        background: vars['--center-channel-bg'],
        color: vars['--center-channel-color'],
        ...(narrow ? {width: '360px'} : {}),
    } as React.CSSProperties;

    return (
        <div
            className='lab-centre'
            data-theme={layout.theme}
            style={style}
        >{children}</div>
    );
};

// Which component Mattermost draws a post's message with: a post's own
// props.type wins over its type (post_message_view.tsx), so an ordinary post
// can ask for the card, and the card has to refuse it. Read carefully: one of
// the samples is a post whose props cannot be read at all.
export function postTypeOf(post: {type?: string; props?: unknown}): string {
    try {
        const props = post.props as {type?: unknown} | undefined;
        if (props && typeof props.type === 'string' && props.type !== '') {
            return props.type;
        }
    } catch {
        // The card's boundary is what has to cope with this one.
    }

    return post.type || '';
}

// A post's message as Mattermost draws it: the plugin's cards for its own two
// types (a shared clip, and since 3.2.0 a shared plan, design/3.2.0.md 6.5),
// and the message as words for everything else.
export const LabPostBody: React.FC<{post: {id?: string; type?: string; message?: string; props?: unknown}}> = ({post}) => {
    const layout = useCardLayout();

    if (postTypeOf(post) === PLAN_POST_TYPE) {
        return (
            <PlanPostCard
                post={post}
                compactDisplay={layout.compact}
                isRHS={false}
                theme={{type: layout.theme === 'denim' ? 'Denim' : 'Onyx'}}
            />
        );
    }

    if (postTypeOf(post) !== CLIP_POST_TYPE) {
        return <div className='lab-shared-text'>{post.message}</div>;
    }

    return (
        <ClipPostCard
            post={post}
            compactDisplay={layout.compact}
            isRHS={false}
            theme={{type: layout.theme === 'denim' ? 'Denim' : 'Onyx'}}
        />
    );
};

// ---- The sample posts ----

type SamplePost = {id: string; type: string; message: string; user_id: string; props?: unknown};

type Sample = {key: string; label: string; note: string; post: SamplePost};

const DAY_MS = 86400000;

// A clip as a share posted it, with what Mattermost and the server did to its
// props since.
function sharedPost(entry: LabClip, more: Record<string, unknown> = {}, drop: string[] = []): SamplePost {
    const props: Record<string, unknown> = {...labShareProps(entry), villanet_share_nonce: '', ...more};
    drop.forEach((key) => {
        delete props[key];
    });

    return {id: `lab-shared-${entry.id}-${Object.keys(more).join('-')}`, type: CLIP_POST_TYPE, message: labShareMessage(entry), user_id: ME_ID, props};
}

// A clip of the lab's own, as it was when it was shared: props are written
// once, at the share, so they do not follow the lab's later changes — which
// a linked reader's card does, from the server.
function sampleClip(world: World, id: string): LabClip {
    const found = world.clips.find((entry) => entry.id === id);
    if (found) {
        return found;
    }

    return {
        id,
        title: 'the one that got away',
        createdAt: Date.now() - (2 * DAY_MS),
        startAt: Date.now() - (2 * DAY_MS),
        durationMs: 72000,
        width: 256,
        height: 144,
        codecs: 'avc1.64000c,opus',
        bytes: 250000,
        clipperId: 'u-erin',
        streamerId: 'u-bob',
        streamName: 'Bob',
        handle: 'h-bob',
        streamKey: 'live-bob',
        voters: {},
        reports: {},
        shares: [],
    };
}

const SCRUBBED = ['villanet_clip_title', 'villanet_clip_clipper', 'villanet_clip_clipper_id', 'villanet_clip_streamer',
    'villanet_clip_streamer_id', 'villanet_clip_tag', 'villanet_clip_tag_kind'];

// The card's states, one post each (addendum 4.12), made from the world as
// the lab starts.
function buildSamples(): Sample[] {
    const world = getWorld();
    const jump = sampleClip(world, '3f9a0c2e7b1d4a55');
    const briefing = sampleClip(world, '5d6e7f8091a2b3c4');
    const bare = sampleClip(world, 'b0c1d2e3f4a5b6c7');
    const summons = sampleClip(world, '0f1e2d3c4b5a6978');
    const racing = sampleClip(world, '9a8b7c6d5e4f3a2b');
    const away = sampleClip(world, 'c0ffee00c0ffee00');
    const aged = {...sampleClip(world, 'dead00beefcafe01'), title: 'last month’s finale', createdAt: Date.now() - (31 * DAY_MS)};

    const crashing = {
        id: 'lab-shared-crash',
        type: CLIP_POST_TYPE,
        message: labShareMessage(jump),
        user_id: ME_ID,
        get props(): unknown {
            throw new Error('These props cannot be read.');
        },
    };

    return [
        {key: 'live', label: 'live, with a picture', note: 'Linked: the server’s card, its picture and the Hytale chip (a game). Unlinked: the same words from the post, the placeholder and no request', post: sharedPost(jump)},
        {key: 'movie', label: 'live, a movie', note: 'Tagged Heat, a movie; from Victor’s own stream', post: sharedPost(briefing)},
        {key: 'bare', label: 'live, no picture', note: 'No picture and no title: the streamer’s face blurred, and the streamer and the time for a title', post: sharedPost(bare)},
        {key: 'expired', label: 'expired (marked)', note: 'villanet_clip_gone "expired", as the server marks it: the title, the names and the words-only tag stay', post: sharedPost(summons, {villanet_clip_gone: 'expired'})},
        {key: 'deleted', label: 'deleted (marked)', note: 'villanet_clip_gone "deleted": the server took the title, the names and the tag out of the props, and the card names nothing', post: sharedPost(racing, {villanet_clip_gone: 'deleted'}, SCRUBBED)},
        {key: 'gone', label: 'gone', note: 'Its id is not in the world: for a linked reader the cards route says null, and the card names nothing. Unlinked readers still see the post’s words', post: sharedPost(away)},
        {key: 'aged', label: 'never marked, past retention', note: 'Made 31 days ago and never marked (an archived channel): kept 30 days, so every reader’s card says expired, with no request', post: sharedPost(aged)},
        {key: 'forged', label: 'forged props.type', note: 'An ordinary post whose props.type asks for the card: Mattermost draws the card’s component, and the card draws the message', post: {id: 'lab-forged', type: '', message: 'Totally a clip, honest: https://example.com/free-clip', user_id: 'u-carol', props: {type: CLIP_POST_TYPE, villanet_clip_id: jump.id, villanet_clip_title: 'forged'}}},
        {key: 'copy', label: 'downgraded by the server', note: 'A clip post the plugin did not make, as its server lets it through: type custom_villanet_clip_copy and every villanet_ prop blanked. No component is drawn for it: the message, as Mattermost writes any', post: {id: 'lab-copy', type: 'custom_villanet_clip_copy', message: labShareMessage(jump), user_id: 'u-carol', props: {type: '', villanet_clip_id: '', villanet_clip_title: ''}}},
        {key: 'wrong', label: 'props of the wrong type', note: 'The length is a string: the card draws the message as plain text', post: {...sharedPost(jump), id: 'lab-wrong', props: {...labShareProps(jump), villanet_clip_duration_ms: '45000'}}},
        {key: 'crash', label: 'crash the card', note: 'Reading the post’s props throws: the boundary draws the message as plain text instead of taking the post with it', post: crashing},
    ];
}

let samples: Sample[] | null = null;

const samplesOf = (): Sample[] => {
    samples = samples || buildSamples();

    return samples;
};

const NAMES: Record<string, string> = {[ME_ID]: 'Victor', 'u-carol': 'Caro'};

const SharedPost: React.FC<{sample: Sample}> = ({sample}) => (
    <div className='lab-shared-post'>
        <div className='lab-shared-head'>
            <span className='lab-shared-name'>{NAMES[sample.post.user_id] || 'Victor'}</span>
            <span className='lab-shared-time'>{sample.label}</span>
        </div>
        <LabPostBody post={sample.post}/>
    </div>
);

// Mattermost's permalink preview round a post, with its click handler copied
// (post_attachment_container.tsx, 11.11): a click whose target is not an A,
// IMG, BUTTON or I, or something with role button, follows the permalink, a
// click on an svg included (11.11 reads an svg's class from its baseVal).
// That is not done here; it is counted and said.
const LabPermalink: React.FC<{children: React.ReactNode}> = ({children}) => {
    const [count, setCount] = React.useState(0);

    const onClick = (event: React.MouseEvent<HTMLDivElement>) => {
        event.stopPropagation();
        const target = event.target as Element;
        if (['A', 'IMG', 'BUTTON', 'I'].includes(target.tagName) || target.getAttribute('role') === 'button') {
            return;
        }

        setCount((value) => value + 1);
        labSay(`navigate: a click on a <${target.tagName.toLowerCase()}> in the preview, and Mattermost would follow the permalink.`);
    };

    return (
        <>
            <div
                className='lab-permalink'
                role='button'
                tabIndex={-1}
                onClick={onClick}
            >
                <span className='lab-permalink-head'>{'Victor · Town Square · a permalink preview'}</span>
                {children}
            </div>
            <span className='lab-note'>{`navigations: ${count} (the ▶, the picture, its clapper, the length and the eyebrow’s icon must not count)`}</span>
        </>
    );
};

// ---- Sharing: the server's answers, and the channel the button points at ----

const SHARE_MODES: Array<[World['shareMode'], string, string]> = [
    ['ok', 'ok', 'Shared: a card arrives in the channel that is open (the post goes into the lab’s store as the server’s would), and the button says Shared until another channel is opened. Again within a minute: the same post, nothing new'],
    ['cannot_post', 'cannot post', 'The server says the reader cannot post there (403 cannot_post)'],
    ['share_cap', 'share cap', 'Already shared in 10 places (409 share_cap)'],
    ['rate', 'rate-limited', 'Too many shares in a row (429)'],
    ['gone', 'gone', 'The clip went before the share (410 clip_gone)'],
    ['gone_on_arrival', 'gone on arrival', 'The clip went while the post was being made: the server takes its post back (410): it shows in the channel for a moment and goes'],
    ['network', 'network', 'No answer: “The clip could not be shared just now.” with Try again, which the server would answer with the same post within a minute'],
];

const PARTNERS: MattermostChannel = {id: 'c-partners', team_id: LAB_TEAM.id, type: 'O', name: 'partners', display_name: 'Partners', shared: true};

const SHARE_INTO: Array<[string, string, string]> = [
    ['c-town', 'Town Square', '“Share in Town Square”'],
    ['', 'no channel', 'The Threads view, where no channel is open: Share is greyed, “Open a channel to share this clip”'],
    ['c-partners', 'shared channel', 'A channel shared with another Mattermost server: Share is greyed, “Clips can’t be shared into a shared channel”'],
    ['c-dm-dave', 'DM label', 'A direct message with Dave: “Share with Dave Hill”, and “Click again to send it to Dave Hill”'],
    ['c-group', 'GM label', 'A group message: “Share in this group message”'],
];

function shareInto(channelId: string): void {
    if (channelId === 'c-partners') {
        labDispatch({type: 'RECEIVED_CHANNEL', data: PARTNERS});
    } else if (channelId === 'c-group') {
        labDispatch({type: 'RECEIVED_CHANNEL', data: LAB_CHANNELS.find((channel) => channel.id === 'c-group')});
    }
    labDispatch({type: 'SELECT_CHANNEL', data: channelId});
}

function useWorldNow(): World {
    const [, bump] = React.useState(0);
    React.useEffect(() => subscribe(() => bump((value) => value + 1)), []);

    return getWorld();
}

// ---- A clip link ----

// Which clip link is open in the lab's centre, and where it stands: the page
// itself ('route'), Mattermost's sign-in page it sent a signed-out reader to
// ('login'), or nothing. key mounts the page afresh, as each link does.
type LinkState = {path: string; stage: 'route' | 'login'; key: number} | null;

let link: LinkState = null;
const linkListeners = new Set<() => void>();

function setLink(next: LinkState): void {
    link = next;
    linkListeners.forEach((listener) => listener());
}

export function useClipLink(): LinkState {
    const [, bump] = React.useState(0);
    React.useEffect(() => {
        const listener = () => bump((value) => value + 1);
        linkListeners.add(listener);

        return () => {
            linkListeners.delete(listener);
        };
    }, []);

    return link;
}

const LINK_PATH = `/plug/${PLUGIN_ID}/clip/3f9a0c2e7b1d4a55`;

export function openLabLink(path: string): void {
    clearLabReplaces();
    setLink({path, stage: 'route', key: Date.now()});
}

// The page itself, in the lab's centre, until it sends Mattermost somewhere:
// to sign in, which the lab draws next, or on to the team, which is the lab's
// channel again with the dialog over it and the panel opened.
export const LabClipLink: React.FC = () => {
    const current = useClipLink();
    const replaces = useLabReplaces();
    const last = replaces[replaces.length - 1] || '';

    React.useEffect(() => {
        if (!current || current.stage !== 'route' || last === '') {
            return;
        }

        if (last.startsWith('/login')) {
            setLink({...current, stage: 'login'});
        } else {
            setLink(null);
        }
    }, [last]);

    if (!current) {
        return null;
    }

    return (
        <div className='lab-cliplink'>
            <div className='lab-cliplink-bar'>{`Address: ${current.path}`}</div>
            {current.stage === 'route' ? (
                <LabCentre>
                    <div className='lab-cliplink-page'>
                        <ClipLinkRoute
                            key={current.key}
                            pathname={current.path}
                        />
                    </div>
                </LabCentre>
            ) : (
                <div className='lab-login'>
                    <p>{'Mattermost’s sign-in page, with the clip link kept to come back to.'}</p>
                    <button
                        type='button'
                        className='lab-chip'
                        onClick={() => {
                            // Signed in with a password: the store says who,
                            // with no reload, and login pushes redirect_to,
                            // which mounts the link's page again.
                            const back = decodeURIComponent(last.replace(/^\/login\?redirect_to=/, ''));
                            change((world) => {
                                world.meWithheld = false;
                            });
                            labDispatch({type: 'RECEIVED_ME', data: {id: ME_ID}});
                            clearLabReplaces();
                            setLink({path: back, stage: 'route', key: Date.now()});
                        }}
                    >{'Sign in as Victor'}</button>
                </div>
            )}
            <ol className='lab-cliplink-log'>
                {replaces.map((path, index) => (
                    <li key={`${index}-${path}`}>{`replace(${path})`}</li>
                ))}
            </ol>
            <button
                type='button'
                className='lab-chip'
                onClick={() => setLink(null)}
            >{'Back to the channel'}</button>
        </div>
    );
};

// ---- The block ----

type BlockProps = {
    // The Access row's own switches, so a link is opened as somebody linked
    // or not, the way that row would set them.
    setLinked: (mode: 'yes' | 'no') => void;
};

export const LabSharedClips: React.FC<BlockProps> = ({setLinked}) => {
    const world = useWorldNow();
    const layout = useCardLayout();
    const labState = useLabState();
    const channelId = labState.entities.channels.currentChannelId;
    const [preview] = React.useState(() => samplesOf()[0]);

    return (
        <div className='lab-shared'>
            <div className='lab-row'>
                <span className='lab-label'>{'Centre theme'}</span>
                <div className='lab-controls'>
                    {(['denim', 'onyx'] as const).map((theme) => (
                        <button
                            key={theme}
                            type='button'
                            className='lab-chip'
                            aria-pressed={layout.theme === theme}
                            title={theme === 'denim' ? 'Mattermost’s default theme: a white channel, dark grey text, blue buttons' : 'Mattermost’s dark theme: a near-black channel, light grey text, lighter blue buttons'}
                            onClick={() => setLayout({theme})}
                        >{theme === 'denim' ? 'Denim' : 'Onyx'}</button>
                    ))}
                    <button
                        type='button'
                        className='lab-chip'
                        aria-pressed={layout.compact}
                        title='Mattermost’s compact message display (compactDisplay): the picture shrinks to 112 × 63'
                        onClick={() => setLayout({compact: !layout.compact})}
                    >{'compactDisplay'}</button>
                    <button
                        type='button'
                        className='lab-chip'
                        aria-pressed={layout.narrow}
                        title='A thread in the side panel, 360px wide'
                        onClick={() => setLayout({narrow: !layout.narrow})}
                    >{'narrow (RHS, 360px)'}</button>
                    <button
                        type='button'
                        className='lab-chip'
                        title='Forget everything the cards were told, as unlinking does, and have every card on show ask again: with Clip fetch on slow, fails or network, states 6 and 8 show; on gone (410), every linked card says the clip has gone'
                        onClick={() => {
                            dropClipCaches();
                            labSay('Everything the cards were told is forgotten; every card on show asks again, in one request.');
                        }}
                    >{'cards ask again'}</button>
                </div>
            </div>

            <div className='lab-row'>
                <span className='lab-label'>{'Share'}</span>
                <div className='lab-controls'>
                    {SHARE_MODES.map(([mode, label, note]) => (
                        <button
                            key={mode}
                            type='button'
                            className='lab-chip'
                            aria-pressed={world.shareMode === mode}
                            title={note}
                            onClick={() => change((current) => {
                                current.shareMode = mode;
                            })}
                        >{label}</button>
                    ))}
                    <button
                        type='button'
                        className='lab-chip'
                        title='The Clips dialog on “that jump”, whose Share sits before Report and Delete. Publishing a clip in the editor (Capture: ✂ clip Dave now) offers it too, between Watch it and Done'
                        onClick={() => openClips('3f9a0c2e7b1d4a55')}
                    >{'open “that jump”'}</button>
                </div>
            </div>

            <div className='lab-row'>
                <span className='lab-label'>{'Share into'}</span>
                <div className='lab-controls'>
                    {SHARE_INTO.map(([id, label, note]) => (
                        <button
                            key={label}
                            type='button'
                            className='lab-chip'
                            aria-pressed={channelId === id}
                            title={`${note}. The channel the lab’s Mattermost has open; the Share button follows it.`}
                            onClick={() => shareInto(id)}
                        >{label}</button>
                    ))}
                </div>
            </div>

            <div className='lab-row'>
                <span className='lab-label'>{'Open a clip link'}</span>
                <div className='lab-controls'>
                    <button
                        type='button'
                        className='lab-chip'
                        title={`${LINK_PATH}, signed in and linked: the page says “Opening the clip in Matty…”, sends Mattermost on to /${LAB_TEAM.name}, and the dialog opens on “that jump” with the panel opened behind it`}
                        onClick={() => {
                            setLinked('yes');
                            openLabLink(LINK_PATH);
                        }}
                    >{'signed in, linked'}</button>
                    <button
                        type='button'
                        className='lab-chip'
                        title='The same link, signed in and locked: the panel opens on the lock, “Unlock Matty to watch this clip.”, with its bar in focus (the link was the person’s own press); unlocking opens the clip'
                        onClick={() => {
                            setLinked('no');
                            openLabLink(LINK_PATH);
                        }}
                    >{'signed in, locked'}</button>
                    <button
                        type='button'
                        className='lab-chip'
                        title='Nobody signed in, as from an email or a phone’s browser: the page sends Mattermost to /login?redirect_to=…, and signing in there comes back to the link, which then opens the clip'
                        onClick={() => {
                            setLinked('yes');
                            labDispatch({type: 'LOGOUT_SUCCESS'});
                            openLabLink(LINK_PATH);
                        }}
                    >{'signed out'}</button>
                    <button
                        type='button'
                        className='lab-chip'
                        title={`/plug/${PLUGIN_ID}/clip/nope: not a clip’s address, so Mattermost is sent to its front page`}
                        onClick={() => openLabLink(`/plug/${PLUGIN_ID}/clip/nope`)}
                    >{'not a clip'}</button>
                </div>
            </div>

            <p className='lab-blurb'>
                {'A shared clip’s post, in each of the card’s states. Access (linked, let in, locked, unknown, and signed in after load) changes who reads them: a locked reader’s card has no picture and no request, and its Watch opens the lock; a thread popout says where to unlock it. Clip fetch changes what the server answers. The counters above count every request the cards make.'}
            </p>

            <LabCentre narrow={layout.narrow}>
                <div className='lab-shared-list'>
                    {samplesOf().map((sample) => (
                        <div
                            key={sample.key}
                            className='lab-shared-item'
                        >
                            <SharedPost sample={sample}/>
                            <p className='lab-shared-note'>{sample.note}</p>
                        </div>
                    ))}
                    <div className='lab-shared-item'>
                        <LabPermalink>
                            <LabPostBody post={preview.post}/>
                        </LabPermalink>
                        <p className='lab-shared-note'>
                            {'The card inside a permalink preview: click the ▶, the picture, the clapper on it, the length on it and the eyebrow’s icon — none may navigate. A click on the words does, as on any preview.'}
                        </p>
                    </div>
                </div>
            </LabCentre>
        </div>
    );
};
