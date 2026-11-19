import React from 'react';

// Inline SVG, stroked with currentColor so the pill styling drives the colour
// in both the resting and hover states.

type IconProps = {
    size?: number;
};

const base = (size: number) => ({
    width: size,
    height: size,
    viewBox: '0 0 24 24',
    fill: 'none' as const,
    stroke: 'currentColor',
    strokeWidth: 2,
    strokeLinecap: 'round' as const,
    strokeLinejoin: 'round' as const,
    'aria-hidden': true,
});

export const SpeakerIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <polygon points='11 5 6 9 2 9 2 15 6 15 11 19 11 5'/>
        <path d='M15.5 8.5a5 5 0 0 1 0 7'/>
        <path d='M18.5 5.5a9 9 0 0 1 0 13'/>
    </svg>
);

export const SpeakerMutedIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <polygon points='11 5 6 9 2 9 2 15 6 15 11 19 11 5'/>
        <line
            x1='22'
            y1='9'
            x2='16'
            y2='15'
        />
        <line
            x1='16'
            y1='9'
            x2='22'
            y2='15'
        />
    </svg>
);

export const FullscreenIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <polyline points='15 3 21 3 21 9'/>
        <polyline points='9 21 3 21 3 15'/>
        <line
            x1='21'
            y1='3'
            x2='14'
            y2='10'
        />
        <line
            x1='3'
            y1='21'
            x2='10'
            y2='14'
        />
    </svg>
);

export const ExitFullscreenIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <polyline points='4 14 10 14 10 20'/>
        <polyline points='20 10 14 10 14 4'/>
        <line
            x1='14'
            y1='10'
            x2='21'
            y2='3'
        />
        <line
            x1='3'
            y1='21'
            x2='10'
            y2='14'
        />
    </svg>
);

export const PopOutIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <path d='M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6'/>
        <polyline points='15 3 21 3 21 9'/>
        <line
            x1='10'
            y1='14'
            x2='21'
            y2='3'
        />
    </svg>
);

export const PlusIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <line
            x1='12'
            y1='5'
            x2='12'
            y2='19'
        />
        <line
            x1='5'
            y1='12'
            x2='19'
            y2='12'
        />
    </svg>
);

export const MinusIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <line
            x1='5'
            y1='12'
            x2='19'
            y2='12'
        />
    </svg>
);

export const CloseIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <line
            x1='6'
            y1='6'
            x2='18'
            y2='18'
        />
        <line
            x1='18'
            y1='6'
            x2='6'
            y2='18'
        />
    </svg>
);

export const ChevronIcon: React.FC<IconProps> = ({size = 12}) => (
    <svg {...base(size)}>
        <polyline points='9 5 16 12 9 19'/>
    </svg>
);

export const TrophyIcon: React.FC<IconProps> = ({size = 18}) => (
    <svg {...base(size)}>
        <path d='M7 4h10v6a5 5 0 0 1-10 0V4Z'/>
        <path d='M17 5h3v2a4 4 0 0 1-4 4'/>
        <path d='M7 5H4v2a4 4 0 0 0 4 4'/>
        <path d='M12 15v4'/>
        <path d='M8.5 21h7'/>
    </svg>
);

export const HeadphonesIcon: React.FC<IconProps> = ({size = 15}) => (
    <svg {...base(size)}>
        <path d='M4 15v-3a8 8 0 0 1 16 0v3'/>
        <path d='M4 15h2.5a1 1 0 0 1 1 1v3a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1Z'/>
        <path d='M20 15h-2.5a1 1 0 0 0-1 1v3a1 1 0 0 0 1 1H19a1 1 0 0 0 1-1Z'/>
    </svg>
);

// The two category icons, and the only FILLED ones here.
//
// Everything else on this page is a stroked outline, which is right for a
// control: a speaker, a cross, a chevron are simple shapes and an outline reads
// at any size. These two are not. They sit at fourteen pixels in a line of
// text, and a controller or a clapperboard is only recognisable by the things
// that do not survive an outline at that size — the grips, the raised stick.
//
// The first attempt was stroked, and the panel showed exactly what that gets
// you: a rounded rectangle with some grey smears in it. Rendered to a canvas at
// fourteen pixels and magnified with smoothing off, there was nothing there to
// recognise. A filled silhouette is legible at the same size because the shape
// itself carries the meaning.
//
// The holes are punched with fill-rule="evenodd" rather than painted in the
// background colour. These are drawn on the card (near-black), on a pill, and
// in the profile popover — which is Mattermost's own furniture and is WHITE for
// anybody on a light theme. A hole that is really a dark shape would be a dark
// shape there.

const filled = (size: number) => ({
    width: size,
    height: size,
    viewBox: '0 0 24 24',
    fill: 'currentColor',
    fillRule: 'evenodd' as const,
    stroke: 'none',
    'aria-hidden': true,
});

// A modern controller, seen head on: a body with two grips flaring down, a
// d-pad and one button. The grips are what make the silhouette a controller
// rather than a lozenge, and the single large button reads where two small ones
// blurred into each other.
export const GameIcon: React.FC<IconProps> = ({size = 15}) => (
    <svg {...filled(size)}>
        <path
            d={'M7.2 6h9.6a6 6 0 0 1 5.9 4.9l.9 4.7a3.3 3.3 0 0 1-6 2.6l-1.9-2.7a2.2 2.2 0 0 0-1.8-.9' +
                'h-3.8a2.2 2.2 0 0 0-1.8.9l-1.9 2.7a3.3 3.3 0 0 1-6-2.6l.9-4.7A6 6 0 0 1 7.2 6z' +
                'M7 8.15h2v2h2v2H9v2H7v-2H5v-2h2z' +
                'M16.1 11.2a1.5 1.5 0 1 0 3 0 1.5 1.5 0 1 0-3 0z'}
        />
    </svg>
);

// A clapperboard with the stick HINGED UP, which is the pose everyone pictures
// and the only one that cannot be mistaken for a box. Closed, it is a rectangle
// with a striped band; open, the wedge of space under the raised stick says
// what it is before the stripes are legible at all.
export const MovieIcon: React.FC<IconProps> = ({size = 15}) => (
    <svg {...filled(size)}>
        <rect
            x='2.4'
            y='11'
            width='19.2'
            height='9.8'
            rx='1.8'
        />
        <path
            d={'M2.8 10.6 2.4 7.2 21.2 2.4 22.2 5.7z' +
                'M5.41 6.43 7.29 5.95 7.84 9.33 5.9 9.82z' +
                'M10.67 5.09 12.55 4.61 13.28 7.95 11.34 8.44z' +
                'M15.94 3.74 17.82 3.26 18.71 6.58 16.77 7.07z'}
        />
    </svg>
);

// A calendar, for a planned stream that has not said whether it is a movie or a
// game. Stroked like the controls rather than filled like the two category
// marks: it stands in for a category instead of being one.
export const ScheduleIcon: React.FC<IconProps> = ({size = 15}) => (
    <svg
        {...base(size)}
        strokeWidth={1.7}
    >
        <rect
            x='3'
            y='5.5'
            width='18'
            height='15.5'
            rx='2'
        />
        <path d='M3 10.5h18'/>
        <path d='M8 3.2v4'/>
        <path d='M16 3.2v4'/>
    </svg>
);

// The switch on a television: a ring broken at the top, and the stroke through
// the gap. Drawn a little heavier than the other controls, because it sits on
// its own in the middle of a picture rather than in a row of its kind.
export const PowerIcon: React.FC<IconProps> = ({size = 22}) => (
    <svg
        {...base(size)}
        strokeWidth={2.4}
    >
        <path d='M7.05 6.55a7.5 7.5 0 1 0 9.9 0'/>
        <line
            x1='12'
            y1='3'
            x2='12'
            y2='11.5'
        />
    </svg>
);

export const CheckIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg
        {...base(size)}
        strokeWidth={2.6}
    >
        <polyline points='5 12.5 10 17.5 19 7'/>
    </svg>
);

export const PencilIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <path d='M4 20h4L19 9a2.8 2.8 0 0 0-4-4L4 16v4Z'/>
        <path d='M13.5 6.5l4 4'/>
    </svg>
);

// An eye, for how many are watching, and the lock's Show token.
export const EyeIcon: React.FC<IconProps> = ({size = 12}) => (
    <svg {...base(size)}>
        <path d='M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12z'/>
        <circle
            cx='12'
            cy='12'
            r='3'
        />
    </svg>
);

// The same eye struck through: the lock's Hide token, while it is shown.
export const EyeOffIcon: React.FC<IconProps> = ({size = 12}) => (
    <svg {...base(size)}>
        <path d='M10.6 5.1A9.8 9.8 0 0 1 12 5c6.4 0 10 7 10 7a17 17 0 0 1-2.7 3.6'/>
        <path d='M6.6 6.6C3.9 8.4 2 12 2 12s3.6 7 10 7a9.5 9.5 0 0 0 5.4-1.6'/>
        <path d='M9.9 9.9a3 3 0 0 0 4.2 4.2'/>
        <path d='M3 3l18 18'/>
    </svg>
);

// An arrow to the right, the way a message box sends: the lock's Unlock,
// at the end of its bar.
export const ArrowRightIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <path d='M5 12h14'/>
        <path d='M13 6l6 6-6 6'/>
    </svg>
);

// Two links of a chain: linking the stream you publish to your account.
// The lock screen's padlock, on the same 24-unit grid. Its shackle is a path
// of its own so the stylesheet can swing it open as the lock opens
// (.vntv-lock-shackle), turning about its right leg.
export const PadlockIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <path
            className='vntv-lock-shackle'
            d='M8 11V7.5a4 4 0 0 1 8 0V11'
        />
        <rect
            x='5'
            y='11'
            width='14'
            height='10'
            rx='2'
        />
        <path d='M12 15v2'/>
    </svg>
);

export const LinkIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <path d='M10 13.5a4.5 4.5 0 0 0 6.4.3l3-3a4.5 4.5 0 0 0-6.4-6.4l-1.2 1.2'/>
        <path d='M14 10.5a4.5 4.5 0 0 0-6.4-.3l-3 3a4.5 4.5 0 0 0 6.4 6.4l1.2-1.2'/>
    </svg>
);

// A hash sign, slanted the way it is written: the hashtag search's heading.
export const HashIcon: React.FC<IconProps> = ({size = 18}) => (
    <svg {...base(size)}>
        <line
            x1='4'
            y1='9'
            x2='20'
            y2='9'
        />
        <line
            x1='4'
            y1='15'
            x2='20'
            y2='15'
        />
        <line
            x1='10'
            y1='3'
            x2='8'
            y2='21'
        />
        <line
            x1='16'
            y1='3'
            x2='14'
            y2='21'
        />
    </svg>
);

// A frame with a small filled square in one corner: the small player's Move,
// which sends it to another corner of the channel.
export const CornerIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <rect
            x='3'
            y='4'
            width='18'
            height='16'
            rx='2'
        />
        <rect
            x='12'
            y='7'
            width='6'
            height='5'
            rx='1'
            fill='currentColor'
        />
    </svg>
);

// The player's frame, with an arrow inside it pointing down to a bar at its
// foot: the small player's Hide, which tucks it away and keeps the streams.
// The frame is what keeps it from reading as a download, which is an arrow
// onto a bare line.
export const HideIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <rect
            x='3'
            y='4'
            width='18'
            height='16'
            rx='2'
        />
        <polyline points='9 9 12 12 15 9'/>
        <line
            x1='8'
            y1='16'
            x2='16'
            y2='16'
        />
    </svg>
);

// An arrow going round to where it started: the small player's Retry in bar
// mode, which has room for an icon where the picture has its Retry pill.
export const RetryIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <path d='M19.25 15.38a8 8 0 1 1-1.12-8.52L21 9.5'/>
        <polyline points='21 4.5 21 9.5 16 9.5'/>
    </svg>
);

// ---- Clips ----

// Scissors: the action of making a clip, on a stream's picture. The
// clapperboard below means clips themselves; the Best Clipper's award is a
// gold clapperboard of its own, in badges.tsx.
export const ScissorsIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <circle
            cx='6'
            cy='6'
            r='3'
        />
        <circle
            cx='6'
            cy='18'
            r='3'
        />
        <line
            x1='20'
            y1='4'
            x2='8.12'
            y2='15.88'
        />
        <line
            x1='14.47'
            y1='14.48'
            x2='20'
            y2='20'
        />
        <line
            x1='8.12'
            y1='8.12'
            x2='12'
            y2='12'
        />
    </svg>
);

// A clapperboard with its arm open: Clips, in the header, the panel's row and
// the dialog's title.
export const ClapperIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <path d='M3 11h18v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z'/>
        <path d='M3 11 2.2 8.1a1.5 1.5 0 0 1 1.1-1.9l13.6-3.6a1.5 1.5 0 0 1 1.9 1.1l.8 2.9z'/>
        <path d='m7.3 5.2 2.4 3.6'/>
        <path d='m12.6 3.8 2.4 3.6'/>
    </svg>
);

// The upvote: a thumbs up. Outlined; the stylesheet fills it once the reader
// has voted. Only the hand fills: the cuff keeps its own fill of none, which
// an inherited fill does not override, so a filled thumb still reads as a
// hand on a sleeve rather than as one red blob. The two are drawn a gap apart
// for the same reason.
export const ThumbsUpIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <rect
            x='2.5'
            y='10'
            width='4'
            height='11'
            rx='1'
            fill='none'
        />
        <path d='M8.5 21V10.5l2.8-6.3a1.9 1.9 0 0 1 3.5 1.2l-.6 4.1h5a2 2 0 0 1 2 2.4l-1.4 7a2.5 2.5 0 0 1-2.5 2.1z'/>
    </svg>
);

// A television with its aerial: What's on?, in the footer's bar, for what is
// on your stream right now.
export const TvIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <rect
            x='2.5'
            y='7'
            width='19'
            height='13.5'
            rx='2.5'
        />
        <path d='M8 2.8 12 7l4-4.2'/>
    </svg>
);

// A calendar with a plus on it: planning a stream, in the footer's bar.
export const CalendarPlusIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <rect
            x='3'
            y='5'
            width='18'
            height='16'
            rx='2.5'
        />
        <path d='M3 10h18'/>
        <path d='M8 3v4'/>
        <path d='M16 3v4'/>
        <path d='M12 13v5'/>
        <path d='M9.5 15.5h5'/>
    </svg>
);

// Twitch's own mark, the Glitch. Victor chose it over the word on
// 2026-10-06 for the Twitch tool in the footer's bar, because at a 304 px
// panel the word took the room the stream title needs, and the mark is one
// people know from their other apps. Since 3.4.0 the tool has left the bar
// (Victor's P1 pick, design/3.4.0.md) and the Glitch is the Twitch row's
// mark in Connected accounts, beside the word TWITCH, the one place it
// shows.
//
// The path is Simple Icons 16.34.0's (CC0; drawn from brand.twitch.tv), drawn
// unaltered and in one colour, as Twitch's brand rules ask: never reshaped,
// outlined, turned or added to, scaled only as a whole, and nothing drawn over
// it. So, unlike its neighbours in the bar, it is filled rather than stroked,
// with the ordinary fill rule its path was drawn for rather than the category
// marks' even-odd; the size is the one thing a caller may change.
// build/check-twitch.mjs holds the path to the letter.
export const TwitchIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg
        width={size}
        height={size}
        viewBox='0 0 24 24'
        fill='currentColor'
        aria-hidden={true}
    >
        <path d='M11.571 4.714h1.715v5.143H11.57zm4.715 0H18v5.143h-1.714zM6 0L1.714 4.286v15.428h5.143V24l4.286-4.286h3.428L22.286 12V0zm14.571 11.143l-3.428 3.428h-3.429l-3 3v-3H6.857V1.714h13.714Z'/>
    </svg>
);

export const PlayIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <polygon points='7 4 19 12 7 20 7 4'/>
    </svg>
);

export const PauseIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <rect
            x='6'
            y='4'
            width='4'
            height='16'
            rx='1'
        />
        <rect
            x='14'
            y='4'
            width='4'
            height='16'
            rx='1'
        />
    </svg>
);

// Two arrows chasing each other round: the editor's loop toggle.
export const LoopIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <polyline points='17 1 21 5 17 9'/>
        <path d='M3 11V9a4 4 0 0 1 4-4h14'/>
        <polyline points='7 23 3 19 7 15'/>
        <path d='M21 13v2a4 4 0 0 1-4 4H3'/>
    </svg>
);

// ---- 3.2.0 ----

// A medal on its ribbon: the achievements' row in a profile
// (design/3.2.0.md 5.7).
export const MedalIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <path d='M8 3h8l-2.6 6.6M8 3l2.6 6.6'/>
        <circle
            cx='12'
            cy='15.5'
            r='5.5'
        />
        <path d='M12 13v5'/>
    </svg>
);

// The soundboard (design/3.2.0.md 7.13): a speaker with its waves, and with
// a slash across it while the reader is muted in Mumble, when the button
// stays so they can open it and read why.
export const SoundboardIcon: React.FC<IconProps & {off?: boolean}> = ({size = 15, off = false}) => (
    <svg {...base(size)}>
        <path d='M11 5 6 9H3v6h3l5 4V5Z'/>
        {off ? (
            <line
                x1='3'
                y1='3'
                x2='21'
                y2='21'
            />
        ) : (
            <>
                <path d='M15.5 8.5a5 5 0 0 1 0 7'/>
                <path d='M18.6 5.4a9.5 9.5 0 0 1 0 13.2'/>
            </>
        )}
    </svg>
);

export const MicIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <rect
            x='9'
            y='2.5'
            width='6'
            height='12'
            rx='3'
        />
        <path d='M5 11a7 7 0 0 0 14 0'/>
        <path d='M12 18v3.5'/>
    </svg>
);

// The clip player (ClipTile.tsx): Download, an arrow down into a tray, and
// Replay, an arrow going back round to the start.
export const DownloadIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <path d='M4 15v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3'/>
        <polyline points='7 10 12 15 17 10'/>
        <path d='M12 15V3'/>
    </svg>
);

export const ReplayIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <path d='M4.75 15.38a8 8 0 1 0 1.12-8.52L3 9.5'/>
        <polyline points='3 4.5 3 9.5 8 9.5'/>
    </svg>
);

export const UploadIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <path d='M4 15v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3'/>
        <polyline points='7 8 12 3 17 8'/>
        <path d='M12 3v12'/>
    </svg>
);

export const InfoIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <circle
            cx='12'
            cy='12'
            r='9'
        />
        <path d='M12 11v5'/>
        <path d='M12 7.5v.01'/>
    </svg>
);

// Mumble out of reach: a plug pulled.
export const UnpluggedIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <path d='M7 17 3 21'/>
        <path d='m15 3-3 3'/>
        <path d='m21 9-3 3'/>
        <path d='m8.5 9.5 6 6-2 2a4.2 4.2 0 0 1-6-6Z'/>
        <path d='m12.5 5.5 6 6'/>
    </svg>
);

// Three dots: more for one of your own sounds.
export const MoreIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <path d='M5 12h.01'/>
        <path d='M12 12h.01'/>
        <path d='M19 12h.01'/>
    </svg>
);

// A face, for changing a sound's emoji.
export const SmileIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <circle
            cx='12'
            cy='12'
            r='9'
        />
        <path d='M8.5 14.5a4.5 4.5 0 0 0 7 0'/>
        <path d='M9 9.5h.01'/>
        <path d='M15 9.5h.01'/>
    </svg>
);

// A bin, for deleting one of your own sounds.
export const BinIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <path d='M4 7h16'/>
        <path d='M9.5 7V4.5h5V7'/>
        <path d='M6.5 7l1 12.5h9l1-12.5'/>
    </svg>
);

export const StopIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <rect
            x='6'
            y='6'
            width='12'
            height='12'
            rx='2'
        />
    </svg>
);

export const BackIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <polyline points='15 5 8 12 15 19'/>
    </svg>
);

// Sharing into the channel that is open: an arrow up out of an open tray.
// The tool on an Up next card that posts its plan as a card in the channel
// (design/3.2.0.md 6.6).
export const ShareIcon: React.FC<IconProps> = ({size = 14}) => (
    <svg {...base(size)}>
        <path d='M12 15V3.5'/>
        <path d='m7.5 8 4.5-4.5L16.5 8'/>
        <path d='M5 12.5V19a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-6.5'/>
    </svg>
);

// Steam's rows (design/3.4.0.md 5.3): a game controller in the bar's line
// style, never Steam's logo, whose brand rules were not read. Rounded grips,
// a d-pad and two buttons drawn as dots with the round cap. The filled
// GameIcon above is a category's mark on a card; this one sits among stroked
// glyphs.
export const GamepadIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <path d='M7 6.5h10a4.5 4.5 0 0 1 4.4 3.6l.9 4.7a2.9 2.9 0 0 1-5.1 2.4L15.6 15H8.4l-1.6 2.2a2.9 2.9 0 0 1-5.1-2.4l.9-4.7A4.5 4.5 0 0 1 7 6.5z'/>
        <path d='M7.5 9.5v3.5'/>
        <path d='M5.75 11.25h3.5'/>
        <path d='M15.5 12.4h.01'/>
        <path d='M17.8 10.1h.01'/>
    </svg>
);

// Hytale's rows: a plain cube, three faces meeting at the near corner, never
// Hytale's logo.
export const CubeIcon: React.FC<IconProps> = ({size = 16}) => (
    <svg {...base(size)}>
        <path d='M12 2.8 20 7.4v9.2l-8 4.6-8-4.6V7.4z'/>
        <path d='M4 7.4 12 12l8-4.6'/>
        <path d='M12 12v9.2'/>
    </svg>
);

// Out of Mattermost, to somebody's Steam profile: one diagonal arrow, the
// "↗" of Watch on Twitch ↗ drawn small enough for an icon-only pill
// (design/3.4.0.md 5.4), so the game's name keeps the room beside it.
export const OutArrowIcon: React.FC<IconProps> = ({size = 11}) => (
    <svg {...base(size)}>
        <path d='M7 17 17 7'/>
        <path d='M8.5 7H17v8.5'/>
    </svg>
);
