import React from 'react';

// The airtime awards: a crown for the all-time board, a calendar for the
// current month, each carrying its place. Two different shapes rather than two
// sizes of the same one, so which board a badge came from is readable at a
// glance instead of from a tooltip. The Snoopers' board has a single award of
// its own, binoculars, further down, and so does Clips: the Best Clipper's
// clapperboard.
//
// Inlined rather than served from /static or bundled as PNG. They are a few
// hundred bytes each this way, they stay crisp at any size and on any display,
// and they cost no request at all — which matters here, because the plugin is
// required to make none.
//
// The artwork is drawn twice: once with a heavy cream stroke, then again with a
// thin dark one. That cream halo is what makes these readable against the
// panel's pure black, and it is why they do not need a drop shadow.

const CREAM = '#FAF7EF';
const INK = '#102338';

// Gold, silver, bronze, indexed by place.
const METAL: Record<number, string> = {1: '#F2C94C', 2: '#C7CFD9', 3: '#DB944F'};

// The supplied artwork asked for Liberation Sans, which is not on most of the
// machines this runs on and cannot be fetched. Any grotesque renders these
// three digits near-identically at this size, so the stack degrades quietly
// instead of falling back to a serif.
const DIGIT_FAMILY = "Arial, 'Liberation Sans', Helvetica, sans-serif";

type Props = {
    rank: number;
    size?: number;
};

// The old emoji medals rendered at 13px, and the monthly one was faded on top
// of that. 22px is the point at which the digit inside the artwork is readable
// rather than merely present, and it still sits comfortably in a card row.
const DEFAULT_SIZE = 22;

const Digit: React.FC<{rank: number}> = ({rank}) => (
    <text
        x='64'
        y='104'
        textAnchor='middle'
        fontFamily={DIGIT_FAMILY}
        fontWeight='bold'
        fontSize='70'
        fill={INK}
    >{String(rank)}</text>
);

const CROWN = 'M18 30L40 48L64 14L88 48L110 30L104 105Q103 115 94 115H34Q25 115 24 105Z';

export const AllTimeBadge: React.FC<Props> = ({rank, size = DEFAULT_SIZE}) => {
    const metal = METAL[rank];
    if (!metal) {
        return null;
    }

    return (
        <svg
            width={size}
            height={size}
            viewBox='0 0 128 128'
            aria-hidden={true}
        >
            <path
                d={CROWN}
                fill={metal}
                stroke={CREAM}
                strokeWidth={14}
                strokeLinejoin='round'
            />
            <path
                d={CROWN}
                fill={metal}
                stroke={INK}
                strokeWidth={6}
                strokeLinejoin='round'
            />
            <Digit rank={rank}/>
        </svg>
    );
};

// The calendar's drawing on its own, so a past month's medal can draw it with
// a star where the place was (PastMonthBadge).
const MonthBadgeArt: React.FC<{rank: number; noDigit?: boolean}> = ({rank, noDigit}) => {
    const metal = METAL[rank];

    return (
        <>
            <rect
                x='12'
                y='24'
                width='104'
                height='96'
                rx='15'
                fill={CREAM}
            />
            <rect
                x='16'
                y='28'
                width='96'
                height='88'
                rx='11'
                fill={INK}
            />
            <path
                d='M22 52H106V104Q106 110 100 110H28Q22 110 22 104Z'
                fill={metal}
            />
            <rect
                x='30'
                y='8'
                width='18'
                height='34'
                rx='9'
                fill={CREAM}
            />
            <rect
                x='34'
                y='12'
                width='10'
                height='26'
                rx='5'
                fill={INK}
            />
            <rect
                x='80'
                y='8'
                width='18'
                height='34'
                rx='9'
                fill={CREAM}
            />
            <rect
                x='84'
                y='12'
                width='10'
                height='26'
                rx='5'
                fill={INK}
            />
            {noDigit ? null : <Digit rank={rank}/>}
        </>
    );
};

export const MonthBadge: React.FC<Props> = ({rank, size = DEFAULT_SIZE}) => {
    if (!METAL[rank]) {
        return null;
    }

    return (
        <svg
            width={size}
            height={size}
            viewBox='0 0 128 128'
            aria-hidden={true}
        >
            <MonthBadgeArt rank={rank}/>
        </svg>
    );
};

// Top Snooper: gold binoculars, for whoever is first on the all-time Snoopers
// board. Drawn the way the crown is, halo and all, so the two read as one set
// beside a name; but in no other metal and with no digit, because there is
// nobody to tell it apart from. The award has one holder: second place on that
// board wins nothing, and neither does the month.
//
// Porro binoculars from the front: eyepieces close together at the top and
// the barrels splaying out to two big lenses. The splay and the dark lenses
// are what make it binoculars rather than two bottles at 16px, where the
// eyepieces are a pixel or two; the lenses are the badge's ink, as the digit
// is the crown's.
const BINOCULARS = 'M38 16H48Q52 16 52 20V32L58 44H70L76 32V20Q76 16 80 16H90Q94 16 94 20V32L116 66V90A23 23 0 0 1 70 90V66H58V90A23 23 0 0 1 12 90V66L34 32V20Q34 16 38 16Z';

export const SnooperBadge: React.FC<{size?: number}> = ({size = DEFAULT_SIZE}) => (
    <svg
        width={size}
        height={size}
        viewBox='0 0 128 128'
        aria-hidden={true}
    >
        <path
            d={BINOCULARS}
            fill={METAL[1]}
            stroke={CREAM}
            strokeWidth={14}
            strokeLinejoin='round'
        />
        <path
            d={BINOCULARS}
            fill={METAL[1]}
            stroke={INK}
            strokeWidth={6}
            strokeLinejoin='round'
        />
        <circle
            cx='35'
            cy='90'
            r='15'
            fill={INK}
        />
        <circle
            cx='93'
            cy='90'
            r='15'
            fill={INK}
        />
    </svg>
);

// Best Clipper: a gold clapperboard, for whoever's clips have the most upvotes
// of all time. Drawn as the crown and the binoculars are, halo first and then
// ink, so the three sit together beside a name; one holder and no digit, like
// the binoculars. The clapper arm is the badge's ink, with three cream
// stripes across it, and the board carries a dark play mark: at 16px the
// stripes and the mark are what say "clip" rather than "box". The line-drawn
// scissors elsewhere are the action of making a clip; this is the award.
// The same drawing is artwork/badges/clip_best.svg.
const CLAP_BODY = 'M16 58H112V108Q112 118 102 118H26Q16 118 16 108Z';
const CLAP_ARM = 'M14 50L104 26L109 44L19 68Z';

export const ClipperBadge: React.FC<{size?: number}> = ({size = DEFAULT_SIZE}) => (
    <svg
        width={size}
        height={size}
        viewBox='0 0 128 128'
        aria-hidden={true}
    >
        <path
            d={CLAP_BODY}
            fill={METAL[1]}
            stroke={CREAM}
            strokeWidth={14}
            strokeLinejoin='round'
        />
        <path
            d={CLAP_ARM}
            fill={INK}
            stroke={CREAM}
            strokeWidth={14}
            strokeLinejoin='round'
        />
        <path
            d={CLAP_BODY}
            fill={METAL[1]}
            stroke={INK}
            strokeWidth={6}
            strokeLinejoin='round'
        />
        <path
            d={CLAP_ARM}
            fill={INK}
            stroke={INK}
            strokeWidth={6}
            strokeLinejoin='round'
        />
        <path
            d='M38 43L50 58M62 37L74 52M86 31L98 46'
            fill='none'
            stroke={CREAM}
            strokeWidth={9}
            strokeLinecap='round'
        />
        <path
            d='M54 74L54 106L84 90Z'
            fill={INK}
            stroke={INK}
            strokeWidth={6}
            strokeLinejoin='round'
        />
    </svg>
);

// On Mumble: headphones on a disc in the theme's accent, with the medals'
// dark rim but not their cream halo — the halo read as a white outline round
// a small round badge, which the medals, being shapes of their own, do not
// have the same trouble with. The accent rather than a red of the plugin's
// own: LIVE keeps the red, so being in voice no longer borrows the colour of
// being on air. It is drawn inside a .vntv root wherever it shows (the
// panel, a post header, the popover), so the token is there; the fallback is
// stock's own button colour. Slate while they are muted, still there but not
// talking — and struck through, because colour alone does not tell the two
// discs apart for everybody.
const VOICE_DISC = 'var(--vntv-accent, #1c58d9)';
const VOICE_MUTED = '#6B7380';

export const VoiceBadge: React.FC<{muted?: boolean; size?: number}> = ({muted, size = DEFAULT_SIZE}) => {
    const disc = muted ? VOICE_MUTED : VOICE_DISC;

    return (
        <svg
            width={size}
            height={size}
            viewBox='0 0 128 128'
            aria-hidden={true}
        >
            <circle
                cx='64'
                cy='64'
                r='56'
                style={{fill: disc}}
                stroke={INK}
                strokeWidth={6}
            />
            <path
                d='M38 74V62a26 26 0 0 1 52 0v12'
                fill='none'
                stroke={CREAM}
                strokeWidth={9}
                strokeLinecap='round'
            />
            <rect
                x='29'
                y='64'
                width='19'
                height='31'
                rx='7'
                fill={CREAM}
            />
            <rect
                x='80'
                y='64'
                width='19'
                height='31'
                rx='7'
                fill={CREAM}
            />
            {muted ? (
                <>
                    {/* The slash in the headphones' own colour, cut out of
                        them by a band of the disc's — no outline of its own. */}
                    <path
                        d='M32 32L96 96'
                        style={{stroke: disc}}
                        strokeWidth={22}
                        strokeLinecap='round'
                    />
                    <path
                        d='M32 32L96 96'
                        stroke={CREAM}
                        strokeWidth={9}
                        strokeLinecap='round'
                    />
                </>
            ) : null}
        </svg>
    );
};

// ---- 3.2.0: the past months, the biggest fan and the achievements ----
// (design/3.2.0.md 2.8, with the art Victor picked from the P4 preview.)

// A past month's medal beside a name: the month board's calendar, at the best
// place somebody took in a month that has ended, with a star where the place
// digit was. The metal still says first, second or third; the star is what
// tells it from this month's calendar, which carries its digit.
const PAST_STAR = 'M64 57L70.9 74.5L89.7 75.4L75.1 87.2L79.9 105.3L64 95L48.1 105.3L52.9 87.2L38.3 75.4L57.1 74.5Z';

export const PastMonthBadge: React.FC<Props> = ({rank, size = DEFAULT_SIZE}) => {
    if (!METAL[rank]) {
        return null;
    }

    return (
        <svg
            width={size}
            height={size}
            viewBox='0 0 128 128'
            aria-hidden={true}
        >
            <MonthBadgeArt
                rank={rank}
                noDigit={true}
            />
            <path
                d={PAST_STAR}
                fill={INK}
            />
        </svg>
    );
};

// Biggest fan: a gold heart, for whoever has watched a streamer the most in
// the panel, all time. One holder per streamer, so gold and no digit, like the
// binoculars and the clapperboard, and drawn the way they are: the cream halo,
// then the ink, then one cream glint on the left lobe. The glint is what keeps
// it a medal rather than an emoji heart at 16px.
const HEART = 'M64 110C36 90 14 70 14 46C14 28 27 16 43 16C53 16 60 22 64 30C68 22 75 16 85 16C101 16 114 28 114 46C114 70 92 90 64 110Z';

export const FanBadge: React.FC<{size?: number}> = ({size = DEFAULT_SIZE}) => (
    <svg
        width={size}
        height={size}
        viewBox='0 0 128 128'
        aria-hidden={true}
    >
        <path
            d={HEART}
            fill={METAL[1]}
            stroke={CREAM}
            strokeWidth={14}
            strokeLinejoin='round'
        />
        <path
            d={HEART}
            fill={METAL[1]}
            stroke={INK}
            strokeWidth={6}
            strokeLinejoin='round'
        />
        <path
            d='M32 47C32 37 38 31 46 30'
            fill='none'
            stroke={CREAM}
            strokeWidth={9}
            strokeLinecap='round'
        />
    </svg>
);

// The achievements: one coin per family, its glyph saying which family and its
// metal saying how far. Coins, where every other award is a shape of its own (a
// crown, a calendar, binoculars, a clapperboard, a heart): beside a name the
// round ones read as "something earned by doing it a lot" and the shaped ones
// as "a title somebody holds", without a tooltip.
//
// The steps, in order: bronze, silver and gold as whole metal coins (a metal
// rim round a dark coin is under a pixel wide at 16px, so the coin's own
// colour is what tells them apart); then the theme's accent as a ring on an
// ink coin, kept to a ring so it never reads as a second On Mumble disc beside
// the headphones; then ink with a gold ring and a gold glyph; past five, a gold
// stud for each step more. The fourth step is the plugin's own among the
// metals, so its ring takes the theme's accent rather than a fixed colour.
// It is a step of the plugin's own, not a metal, so it does not stay red by
// name the way gold, silver and bronze stay what they are. The halo pass and the ink pass are the set's, so a
// coin sits beside the crown at the same weight.
export type AchievementGlyph = 'streamed' | 'snooped' | 'podium' | 'thumbs' | 'channel';

type CoinTier = {body: string; ring: string; ringWidth: number; glyph: string};

const BRONZE_LINE = '#A5642C';
const SILVER_LINE = '#8B96A5';
const GOLD_LINE = '#C99A22';
const COIN_ACCENT = 'var(--vntv-accent, #1c58d9)';

function coinTier(tier: number): CoinTier {
    switch (tier) {
    case 1:
        return {body: METAL[3], ring: BRONZE_LINE, ringWidth: 5, glyph: INK};
    case 2:
        return {body: METAL[2], ring: SILVER_LINE, ringWidth: 5, glyph: INK};
    case 3:
        return {body: METAL[1], ring: GOLD_LINE, ringWidth: 5, glyph: INK};
    case 4:
        // A ring wide enough to read as a colour at 16px (about a pixel and a
        // half), on the same ink as the fifth step's, whose ring is gold.
        return {body: INK, ring: COIN_ACCENT, ringWidth: 12, glyph: CREAM};
    default:
        return {body: INK, ring: METAL[1], ringWidth: 6, glyph: METAL[1]};
    }
}

// The glyphs, on the coin's 128 grid, each inside a circle of about 32 round
// the middle so they clear the ring. Strokes no thinner than 8: at 16px a unit
// is an eighth of a pixel.
const Glyph: React.FC<{kind: AchievementGlyph; color: string; cut: string}> = ({kind, color, cut}) => {
    switch (kind) {
    case 'streamed':
        // ON AIR: a mast sending, its waves either side of the light on top.
        return (
            <g fill={color}>
                <path d='M64 52L52 95H76Z'/>
                <circle
                    cx='64'
                    cy='45'
                    r='8'
                />
                <path
                    d='M54.1 35.1A14 14 0 0 0 54.1 54.9M73.9 35.1A14 14 0 0 1 73.9 54.9M46.3 27.3A25 25 0 0 0 46.3 62.7M81.7 27.3A25 25 0 0 1 81.7 62.7'
                    fill='none'
                    stroke={color}
                    strokeWidth={8}
                    strokeLinecap='round'
                />
            </g>
        );
    case 'snooped':
        // SNOOPER: an eye, for hours spent watching. The binoculars are the
        // Top Snooper's title; a coin is for the hours, and an eye is what
        // still reads as watching at 16px, where small binoculars read as a
        // face with ears.
        return (
            <g>
                <path
                    d='M26 64Q64 20 102 64Q64 108 26 64Z'
                    fill={color}
                />
                <circle
                    cx='64'
                    cy='64'
                    r='17'
                    fill={cut}
                />
                <circle
                    cx='64'
                    cy='64'
                    r='9.5'
                    fill={color}
                />
            </g>
        );
    case 'podium':
        // PODIUM: the podium itself, first place in the middle, with a star
        // over it. A laurel round a star read as a smiling face below 22px.
        return (
            <g fill={color}>
                <rect
                    x='29'
                    y='71'
                    width='22'
                    height='24'
                    rx='2'
                />
                <rect
                    x='53'
                    y='57'
                    width='22'
                    height='38'
                    rx='2'
                />
                <rect
                    x='77'
                    y='79'
                    width='22'
                    height='16'
                    rx='2'
                />
                <path d='M64 27L67.5 36.2L77.3 36.6L69.6 42.7L72.2 52.1L64 46.7L55.8 52.1L58.4 42.7L50.7 36.6L60.5 36.2Z'/>
            </g>
        );
    case 'thumbs':
        // CLIPPER: the thumb the clips are voted with, filled.
        return (
            <g
                fill={color}
                transform='translate(30.5 30) scale(2.75)'
            >
                <rect
                    x='2.5'
                    y='10'
                    width='4.4'
                    height='11'
                    rx='1.2'
                />
                <path d='M8.6 21V10.5l2.8-6.3a1.9 1.9 0 0 1 3.5 1.2l-.6 4.1h5a2 2 0 0 1 2 2.4l-1.4 7a2.5 2.5 0 0 1-2.5 2.1z'/>
            </g>
        );
    default:
        // A 24/7 channel: a crescent moon, for the hours left playing late,
        // and a small star in its bite.
        return (
            <g fill={color}>
                <path d='M61.9 35.1A31 31 0 1 0 89 76.9A25 25 0 0 1 61.9 35.1Z'/>
                <path d='M86 31L88.6 39.4L97 42L88.6 44.6L86 53L83.4 44.6L75 42L83.4 39.4Z'/>
            </g>
        );
    }
};

// Which glyph a family is drawn with. The 24/7 channels' families are an
// admin's, named "ch-" and a hash, and all share the moon.
export function glyphOf(kindId: string): AchievementGlyph {
    switch (kindId) {
    case 'streamed':
    case 'snooped':
    case 'podium':
    case 'thumbs':
        return kindId;
    default:
        return 'channel';
    }
}

export const AchievementBadge: React.FC<{kind: AchievementGlyph; tier: number; size?: number}> = ({kind, tier, size = DEFAULT_SIZE}) => {
    const look = coinTier(tier);
    const pips = Math.max(0, Math.min(3, tier - 5));

    return (
        <svg
            width={size}
            height={size}
            viewBox='0 0 128 128'
            aria-hidden={true}
        >
            <circle
                cx='64'
                cy='64'
                r='52'
                fill={look.body}
                stroke={CREAM}
                strokeWidth={14}
            />
            <circle
                cx='64'
                cy='64'
                r='52'
                fill={look.body}
                stroke={INK}
                strokeWidth={6}
            />
            <circle
                cx='64'
                cy='64'
                r='43'
                fill='none'
                style={{stroke: look.ring}}
                strokeWidth={look.ringWidth}
            />
            <Glyph
                kind={kind}
                color={look.glyph}
                cut={look.body}
            />
            {/* Past five steps: a gold stud on the rim for each one more,
                at the bottom, where the halo gives it an edge. */}
            {Array.from({length: pips}, (_, index) => (
                <circle
                    key={index}
                    cx={64 + ((index - ((pips - 1) / 2)) * 18)}
                    cy='113'
                    r='8.5'
                    fill={METAL[1]}
                    stroke={INK}
                    strokeWidth={3.5}
                />
            ))}
        </svg>
    );
};
