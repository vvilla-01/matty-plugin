import React from 'react';

import {clampIn, clampOut, clampPosition, trimKey, trimReadout, trimValueText} from '../clipLogic';
import type {Trim, TrimThumb} from '../clipLogic';

// The trimmer under the clip editor's picture (spec 3.7): one track the length
// of the draft, the part that will be published lit and the rest dimmed, with
// three thumbs on it — the start, the playhead and the end. It only ever
// shrinks the clip: the draft is all there is, and the start is exact, so it
// has no keyframe marks to snap to.
//
// The thumbs are the WAI-ARIA multi-thumb slider: three role="slider"
// elements, each a Tab stop, whose ranges are the others' positions so the
// handles can never cross or make a clip shorter than five seconds (the
// clamps are clipLogic.ts's, which check-away.mjs checks). Arrows move a
// second, ten with Shift, Page Up and Page Down ten, Home and End to the ends
// of the thumb's own range. Space plays and pauses, and I and O set the start
// and the end at the playhead, from any thumb as from the picture.
//
// Dragging a handle pauses the clip and shows the frame under it; let go, it
// stays paused there. A press on the bare track moves the playhead there, and
// dragging from it scrubs; it never moves a handle. The pointer is captured
// for the drag (ImageCropper's pattern), and given back here when Escape
// cancels it: the editor's capture-phase Escape handler, which no React
// handler would ever see Escape past, asks through control.cancelDrag, and
// the drag goes back to where it began.
//
// The grips are drawn 12px wide; what takes the pointer is 44 × 44 round
// each, the start's reaching out to the left and the end's to the right, so
// two handles five seconds apart on a narrow track can still both be caught.
// touch-action: none on the track, so a drag on a phone is a drag rather than
// the page scrolling.

export type TrimmerControl = {
    // Puts a drag in progress back where it began, and says whether there was
    // one.
    cancelDrag: () => boolean;
};

type Props = {
    durationMs: number;
    inMs: number;
    outMs: number;
    positionMs: number;
    onChange: (inMs: number, outMs: number) => void;
    onSeek: (ms: number) => void;
    onScrubStart: () => void;
    onScrubEnd: () => void;
    onTogglePlay: () => void;
    onMark: (edge: 'in' | 'out') => void;
    control?: React.MutableRefObject<TrimmerControl | null>;
};

type Drag = {
    thumb: TrimThumb;
    pointerId: number;

    // How far from the thumb's own value the pointer took hold of it, in
    // pixels, so the handle does not jump to the pointer.
    offset: number;

    // Where everything was, for Escape.
    from: {inMs: number; outMs: number; positionMs: number};
};

const LABELS: Record<TrimThumb, string> = {
    start: 'Start of the clip',
    position: 'Position',
    end: 'End of the clip',
};

const percent = (ms: number, durationMs: number): string => `${durationMs > 0 ? Math.min(100, Math.max(0, (ms / durationMs) * 100)) : 0}%`;

export const ClipTrimmer: React.FC<Props> = (props) => {
    const {durationMs, inMs, outMs, positionMs, control} = props;
    const trim: Trim = {inMs, outMs, durationMs};
    const track = React.useRef<HTMLDivElement | null>(null);
    const drag = React.useRef<Drag | null>(null);

    // The latest of everything, for the pointer handlers and for Escape,
    // which run between renders.
    const latest = React.useRef({props, trim});
    latest.current = {props, trim};

    const valueOf = (thumb: TrimThumb, at: Trim, position: number): number => {
        if (thumb === 'start') {
            return at.inMs;
        }

        return thumb === 'end' ? at.outMs : position;
    };

    // A thumb moved to a value, already clamped. A handle shows the frame it
    // is on; the playhead is where playback goes.
    const move = (thumb: TrimThumb, value: number) => {
        const {props: now, trim: at} = latest.current;
        if (thumb === 'start') {
            now.onChange(value, at.outMs);
        } else if (thumb === 'end') {
            now.onChange(at.inMs, value);
        }
        now.onSeek(value);
    };

    const msAt = (clientX: number): number => {
        const box = track.current?.getBoundingClientRect();
        if (!box || box.width <= 0) {
            return 0;
        }

        return ((clientX - box.left) / box.width) * latest.current.trim.durationMs;
    };

    const pixelsOf = (ms: number): number => {
        const box = track.current?.getBoundingClientRect();
        const duration = latest.current.trim.durationMs;

        return box && duration > 0 ? box.left + ((ms / duration) * box.width) : 0;
    };

    const clampFor = (thumb: TrimThumb, ms: number): number => {
        const at = latest.current.trim;
        if (thumb === 'start') {
            return clampIn(ms, at);
        }

        return thumb === 'end' ? clampOut(ms, at) : clampPosition(ms, at.durationMs);
    };

    const endDrag = (cancelled: boolean): boolean => {
        const current = drag.current;
        if (!current) {
            return false;
        }

        drag.current = null;
        const element = track.current;
        if (element?.hasPointerCapture?.(current.pointerId)) {
            element.releasePointerCapture(current.pointerId);
        }
        if (cancelled) {
            const {props: now} = latest.current;
            now.onChange(current.from.inMs, current.from.outMs);
            now.onSeek(current.from.positionMs);
        }
        latest.current.props.onScrubEnd();

        return true;
    };

    React.useEffect(() => {
        if (!control) {
            return undefined;
        }

        control.current = {cancelDrag: () => endDrag(true)};

        return () => {
            control.current = null;
        };
    }, [control]);

    const onPointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
        if (event.button !== 0 || drag.current || latest.current.trim.durationMs <= 0) {
            return;
        }

        const grabbed = (event.target as HTMLElement).closest<HTMLElement>('[data-thumb]');
        const thumb = (grabbed?.dataset.thumb as TrimThumb | undefined) || 'position';
        const {trim: at, props: now} = latest.current;
        const value = valueOf(thumb, at, now.positionMs);

        event.preventDefault();
        (grabbed || track.current?.querySelector<HTMLElement>('[data-thumb="position"]'))?.focus({preventScroll: true});

        drag.current = {
            thumb,
            pointerId: event.pointerId,
            offset: grabbed ? event.clientX - pixelsOf(value) : 0,
            from: {inMs: at.inMs, outMs: at.outMs, positionMs: now.positionMs},
        };
        event.currentTarget.setPointerCapture?.(event.pointerId);
        now.onScrubStart();

        // A press on the bare track goes there at once.
        if (!grabbed) {
            move('position', clampFor('position', msAt(event.clientX)));
        }
    };

    const onPointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
        const current = drag.current;
        if (!current || current.pointerId !== event.pointerId) {
            return;
        }

        move(current.thumb, clampFor(current.thumb, msAt(event.clientX - current.offset)));
    };

    const onPointerUp = (event: React.PointerEvent<HTMLDivElement>) => {
        if (drag.current?.pointerId === event.pointerId) {
            endDrag(false);
        }
    };

    const onKeyDown = (thumb: TrimThumb) => (event: React.KeyboardEvent<HTMLElement>) => {
        if (event.ctrlKey || event.metaKey || event.altKey) {
            return;
        }

        const {props: now, trim: at} = latest.current;
        const key = event.key;
        if (key === ' ' || key === 'Spacebar') {
            event.preventDefault();
            event.stopPropagation();
            now.onTogglePlay();

            return;
        }
        if (key === 'i' || key === 'I' || key === 'o' || key === 'O') {
            event.preventDefault();
            event.stopPropagation();
            now.onMark(key.toLowerCase() === 'i' ? 'in' : 'out');

            return;
        }

        const next = trimKey(thumb, key, event.shiftKey, valueOf(thumb, at, now.positionMs), at);
        if (next === null) {
            return;
        }

        event.preventDefault();
        event.stopPropagation();

        // A handle moved from the keyboard shows its frame, as a drag does;
        // the playhead just goes there.
        if (thumb !== 'position') {
            now.onScrubStart();
        }
        move(thumb, next);
        if (thumb !== 'position') {
            now.onScrubEnd();
        }
    };

    // Each thumb's range is the others' positions (the multi-thumb pattern).
    const ranges: Record<TrimThumb, {min: number; max: number; value: number}> = {
        start: {min: 0, max: Math.max(0, outMs - 5000), value: inMs},
        position: {min: 0, max: durationMs, value: positionMs},
        end: {min: Math.min(durationMs, inMs + 5000), max: durationMs, value: outMs},
    };

    const thumbOf = (thumb: TrimThumb, className: string) => (
        <span
            className={`vntv-trim-thumb ${className}`}
            role='slider'
            tabIndex={0}
            data-thumb={thumb}
            aria-label={LABELS[thumb]}
            aria-orientation='horizontal'
            aria-valuemin={Math.round(ranges[thumb].min)}
            aria-valuemax={Math.round(ranges[thumb].max)}
            aria-valuenow={Math.round(ranges[thumb].value)}
            aria-valuetext={trimValueText(thumb, trim, positionMs)}
            style={{left: percent(ranges[thumb].value, durationMs)}}
            onKeyDown={onKeyDown(thumb)}
        />
    );

    return (
        <div className='vntv-trim'>
            <div
                ref={track}
                className='vntv-trim-track'
                onPointerDown={onPointerDown}
                onPointerMove={onPointerMove}
                onPointerUp={onPointerUp}
                onPointerCancel={onPointerUp}
                onLostPointerCapture={() => endDrag(false)}
            >
                <span
                    className='vntv-trim-dim'
                    style={{left: 0, width: percent(inMs, durationMs)}}
                />
                <span
                    className='vntv-trim-sel'
                    style={{left: percent(inMs, durationMs), width: percent(outMs - inMs, durationMs)}}
                />
                <span
                    className='vntv-trim-dim'
                    style={{left: percent(outMs, durationMs), right: 0}}
                />

                {/* Left to right, which is the order Tab takes them in:
                    the start, the playhead, the end (spec 3.7's table).
                    Where they meet the handles lie over the playhead, by
                    their z-index (styles.ts), not by this order: a handle is
                    the harder of the two to reach again. */}
                {thumbOf('start', 'vntv-trim-thumb-start')}
                {thumbOf('position', 'vntv-trim-head')}
                {thumbOf('end', 'vntv-trim-thumb-end')}
            </div>
            <p className='vntv-trim-readout'>{trimReadout(trim)}</p>
        </div>
    );
};
