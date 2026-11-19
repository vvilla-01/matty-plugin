import React from 'react';

import {staticURL} from '../manifest';
import {GHOST_INTERVALS, GHOST_MS, reducedMotion} from '../motion';
import {useArtwork} from '../name';

import {TileInfo} from './Hud';

// The screen when there is nothing on it.
//
// Two states share one look, because they are the same idea: nothing is tuned
// in, and a stream that has dropped off the air. Both get analogue snow, a
// rolling scan and a plate in the middle, and both keep the player's own
// chrome so the panel does not change shape when a stream arrives.
//
// The idle tile has no controls at all — no switch, no sound, no full screen.
// With nothing tuned in there is nothing for any of them to do, and greyed-out
// copies of them over a screen that is only showing static were clutter.
// Pointing at it does nothing; the NO SIGNAL plate stays put.

// Screen is the 16:9 frame: whatever is on it, and — when there is nothing on
// it — the snow and the scanlines. The player puts its <video> in one of these
// too, so a picture and the absence of one are the same rectangle.
//
// noise is off for a stream that is actually playing. Both layers are absolutely
// positioned and therefore paint ON TOP of the video, so leaving them on meant
// scanlines over somebody's broadcast — a television effect applied to the one
// thing that is not a television effect. There is nothing to dress up once
// there is a picture: the picture is the point.
//
// power plays the tube switching on or off. It is on the screen rather than the
// tile because that is what a CRT does: the glass goes to a line and a dot, and
// the cabinet around it — the title bar, the controls — stays exactly where it
// was.
//
// ghost is the picture that flickers through the static now and then: the
// server's own, only when it has one (name.ts useArtwork). It is drawn
// BENEATH the snow, so the static stays on top of it the way it does on a set
// that has only half found a channel.
type ScreenProps = {
    children?: React.ReactNode;
    noise?: boolean;
    power?: 'on' | 'off';
    ghost?: boolean;
    receiving?: boolean;
};

export const Screen: React.FC<ScreenProps> = ({children, noise = true, power, ghost, receiving}) => (
    <div
        className='vntv-screen'
        data-power={power}
        data-ghost={receiving ? 'true' : undefined}
    >
        {ghost && (
            <img
                className='vntv-ghost'
                src={staticURL('brand/ghost')}
                alt=''
                aria-hidden={true}
                draggable={false}
            />
        )}
        {noise && (
            <>
                <span
                    className='vntv-snow'
                    aria-hidden={true}
                />
                <span
                    className='vntv-scan'
                    aria-hidden={true}
                />
            </>
        )}
        {children}
    </div>
);

// Whether the set has caught something this instant.
//
// A random wait between each one, from the range the admin picked, and never
// while the tab is hidden: a timer firing in a background tab is a timer
// nobody is watching. Off entirely for anybody who asked for less motion.
export function useGhost(frequency?: string): boolean {
    const [receiving, setReceiving] = React.useState(false);

    React.useEffect(() => {
        const range = frequency ? GHOST_INTERVALS[frequency] : undefined;
        if (!range || reducedMotion()) {
            setReceiving(false);

            return undefined;
        }

        let timer = 0;

        const wait = () => {
            const delay = range[0] + (Math.random() * (range[1] - range[0]));

            timer = window.setTimeout(() => {
                if (document.hidden) {
                    wait();

                    return;
                }

                setReceiving(true);
                timer = window.setTimeout(() => {
                    setReceiving(false);
                    wait();
                }, GHOST_MS);
            }, delay);
        };

        wait();

        return () => window.clearTimeout(timer);
    }, [frequency]);

    return receiving;
}

// The plate: the words in the middle of the screen, in the display face, boxed
// and letter-spaced like something a television would put there itself.
export const Plate: React.FC<{title: string; note?: string}> = ({title, note}) => (
    <span className='vntv-plate'>
        <span className='vntv-plate-title'>{title}</span>
        {note ? <span className='vntv-plate-note'>{note}</span> : null}
    </span>
);

// What the picture does between channels: the snow comes up, a bright band
// rolls down the screen, and it holds there until the new stream actually
// arrives. It is not on a timer — it ends when there is a picture, which is the
// only honest thing for it to mean.
//
// word is what the plate says: "Tuning" on a stream, "Loading" on a clip
// whose file has still to come (ClipTile.tsx).
export const Tuning: React.FC<{title: string; word?: string}> = ({title, word = 'Tuning'}) => (
    <div className='vntv-tuning'>
        <span
            className='vntv-snow vntv-snow-fast'
            aria-hidden={true}
        />
        <span
            className='vntv-roll'
            aria-hidden={true}
        />
        <span className='vntv-scan'/>
        <Plate
            title={word}
            note={title}
        />
    </div>
);

// The idle tile. Always on screen when nothing is being watched, so the panel
// has a television in it rather than a gap where one goes.
//
// powerOn plays the tube warming up. Only the first time the set appears after
// the panel opens — every other time it is here because a stream was just
// switched off, and a set that switches itself straight back on after you
// turned it off looks broken rather than charming. Those times it fades in.
type NoSignalProps = {
    note?: string;
    powerOn?: boolean;
    ghost?: string;
};

export const NoSignalTile: React.FC<NoSignalProps> = ({note, powerOn, ghost: frequency}) => {
    // How often the admin asked for a ghost, and only on a server that has
    // one: without it the set shows only static, and never a gap in it.
    const ghost = useArtwork().ghost ? frequency : undefined;
    const receiving = useGhost(ghost);

    // Decided once, when the set appears, and kept. The panel stops booting a
    // moment later and passes false from then on; taking that at face value
    // would pull the attribute the animation hangs on out from under it
    // halfway through, and the tube would snap on instead of warming up.
    const [warm] = React.useState(() => Boolean(powerOn) && !reducedMotion());

    return (
        <div
            className='vntv-player vntv-player-dead'
            data-arrive={warm ? undefined : 'fade'}
        >
            <Screen
                power={warm ? 'on' : undefined}
                ghost={Boolean(ghost && ghost !== 'off')}
                receiving={receiving}
            >
                <Plate
                    title='No Signal'
                    note={note}
                />
            </Screen>
            <TileInfo
                name='No Signal'
                dead={true}
            />
        </div>
    );
};
