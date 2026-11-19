import React from 'react';

// Picking a moment: a calendar, then a clock.
//
// Two steps, the way a phone does it. Choose a day from a month grid, and the
// grid is replaced by a dial — outer ring for the morning, inner ring for the
// afternoon and evening — then by the same dial marked in five-minute steps.
// Choosing the minutes is the last thing, so that closes it.
//
// From the keyboard: the days are buttons, and once one is chosen focus moves
// onto the dial, which is a slider — the arrow keys move it to the next hour
// or five minutes that is still open, and Enter goes on to the minutes, then
// closes. Otherwise choosing a day unmounts the button that had focus, and
// focus falls out of the panel with nowhere to go.
//
// Nothing here is typed and nothing here is invalid. A day with no time left in
// it cannot be clicked, an hour that has gone cannot be clicked, and neither can
// anything past the ninety days the server accepts. The caller hands in the
// window and this offers what is inside it.

type Props = {
    value: number;
    earliest: number;
    latest: number;
    onChange: (next: number) => void;
    onClose: () => void;
};

type Step = 'date' | 'hour' | 'minute';

const MINUTE_STEP = 5;

// The dial, in its own 200x200 world. Twelve positions, 0 at the top.
const CENTRE = 100;
const OUTER = 78;
const INNER = 50;
const KNOB = 15;

function startOfDay(at: Date): Date {
    const day = new Date(at);
    day.setHours(0, 0, 0, 0);

    return day;
}

// Field by field rather than by adding milliseconds: a day containing a clock
// change is 23 or 25 hours long.
function atTime(day: Date, hour: number, minute: number): number {
    return new Date(day.getFullYear(), day.getMonth(), day.getDate(), hour, minute, 0, 0).getTime();
}

function pointAt(index: number, radius: number): {x: number; y: number} {
    const radians = ((index * 30) - 90) * (Math.PI / 180);

    return {
        x: CENTRE + (radius * Math.cos(radians)),
        y: CENTRE + (radius * Math.sin(radians)),
    };
}

const pad = (value: number) => String(value).padStart(2, '0');

// Which day a week starts on, asked of the browser and not assumed. Monday for
// most of the world, Sunday in the Americas, and the only thing that makes a
// month grid line up under the right letters.
function firstWeekday(): number {
    try {
        const locale = new Intl.Locale(
            (typeof navigator === 'undefined' ? 'en-GB' : navigator.language) || 'en-GB',
        ) as Intl.Locale & {weekInfo?: {firstDay: number}; getWeekInfo?: () => {firstDay: number}};

        const info = typeof locale.getWeekInfo === 'function' ? locale.getWeekInfo() : locale.weekInfo;
        if (info && info.firstDay >= 1 && info.firstDay <= 7) {
            return info.firstDay % 7;
        }
    } catch {
        // Older engines have neither; Monday is the better guess here.
    }

    return 1;
}

export const WhenPicker: React.FC<Props> = ({value, earliest, latest, onChange, onClose}) => {
    const chosen = new Date(value);
    const day = startOfDay(chosen);
    const hour = chosen.getHours();
    const minute = Math.round(chosen.getMinutes() / MINUTE_STEP) * MINUTE_STEP;

    const [step, setStep] = React.useState<Step>('date');
    const [month, setMonth] = React.useState(() => new Date(chosen.getFullYear(), chosen.getMonth(), 1));
    const [dragging, setDragging] = React.useState(false);

    const dial = React.useRef<SVGSVGElement>(null);

    // Set when the calendar gives way to the dial, so the dial takes focus
    // once it has been drawn.
    const focusDial = React.useRef(false);
    React.useEffect(() => {
        if (focusDial.current && step !== 'date') {
            focusDial.current = false;
            dial.current?.focus();
        }
    }, [step]);

    const inWindow = React.useCallback(
        (at: number) => at >= earliest && at <= latest,
        [earliest, latest],
    );

    // A day is offerable while any five-minute mark on it is inside the window.
    const dayOpen = React.useCallback(
        (candidate: Date) => atTime(candidate, 23, 55) >= earliest && atTime(candidate, 0, 0) <= latest,
        [earliest, latest],
    );

    const hourOpen = React.useCallback(
        (candidate: number) => atTime(day, candidate, 55) >= earliest && atTime(day, candidate, 0) <= latest,
        [day, earliest, latest],
    );

    const minuteOpen = React.useCallback(
        (candidate: number) => inWindow(atTime(day, hour, candidate)),
        [day, hour, inWindow],
    );

    // Moving to another day, or to another hour, can leave the rest of the
    // choice outside the window. Slide to the first mark that is inside it
    // rather than handing back a moment nothing will accept.
    const settle = React.useCallback((nextDay: Date, nextHour: number, nextMinute: number) => {
        for (let step = 0; step < 24; step++) {
            const tryHour = (nextHour + step) % 24;

            // The asked-for hour is searched from the asked-for minute; every
            // hour after it from the top.
            for (let m = step === 0 ? nextMinute : 0; m < 60; m += MINUTE_STEP) {
                if (inWindow(atTime(nextDay, tryHour, m))) {
                    return atTime(nextDay, tryHour, m);
                }
            }
        }

        return 0;
    }, [inWindow]);

    const pickDay = React.useCallback((candidate: Date) => {
        const settled = settle(candidate, hour, minute);
        if (settled === 0) {
            return;
        }

        onChange(settled);
        focusDial.current = true;
        setStep('hour');
    }, [hour, minute, onChange, settle]);


    const pickHour = React.useCallback((candidate: number) => {
        if (!hourOpen(candidate)) {
            return;
        }

        // Keep the minutes already chosen when that hour can still take them,
        // and search the hour from the top when it cannot. Searching from a
        // minute the window has ruled out would walk straight past the part of
        // the hour that is still available — which happens at the far end,
        // where 21:00 is inside ninety days and 21:45 is not.
        const settled = settle(day, candidate, inWindow(atTime(day, candidate, minute)) ? minute : 0);
        if (settled === 0) {
            return;
        }

        onChange(settled);
    }, [day, minute, hourOpen, inWindow, settle, onChange]);

    const pickMinute = React.useCallback((candidate: number) => {
        if (!minuteOpen(candidate)) {
            return;
        }

        onChange(atTime(day, hour, candidate));
    }, [day, hour, minuteOpen, onChange]);

    // The next hour, or five minutes, in a direction that is still open.
    const nudge = React.useCallback((direction: 1 | -1) => {
        if (step === 'hour') {
            for (let n = 1; n < 24; n++) {
                const candidate = (hour + (direction * n) + 24) % 24;
                if (hourOpen(candidate)) {
                    pickHour(candidate);
                    return;
                }
            }

            return;
        }

        for (let n = 1; n < 12; n++) {
            const candidate = (minute + (direction * n * MINUTE_STEP) + 60) % 60;
            if (minuteOpen(candidate)) {
                pickMinute(candidate);
                return;
            }
        }
    }, [step, hour, minute, hourOpen, minuteOpen, pickHour, pickMinute]);

    // Where on the face a pointer is: which of the twelve marks, and which ring.
    const markUnder = React.useCallback((clientX: number, clientY: number) => {
        const svg = dial.current;
        if (!svg) {
            return null;
        }

        const box = svg.getBoundingClientRect();
        if (box.width === 0 || box.height === 0) {
            return null;
        }

        const x = (((clientX - box.left) / box.width) * 200) - CENTRE;
        const y = (((clientY - box.top) / box.height) * 200) - CENTRE;

        const degrees = ((((Math.atan2(y, x) * 180) / Math.PI) + 90) % 360 + 360) % 360;

        return {
            index: Math.round(degrees / 30) % 12,
            inner: Math.hypot(x, y) < ((OUTER + INNER) / 2),
        };
    }, []);

    const aimAt = React.useCallback((clientX: number, clientY: number) => {
        const mark = markUnder(clientX, clientY);
        if (!mark) {
            return;
        }

        if (step === 'hour') {
            pickHour(mark.inner ? mark.index + 12 : mark.index);
            return;
        }

        pickMinute(mark.index * MINUTE_STEP);
    }, [markUnder, step, pickHour, pickMinute]);

    const monthStart = new Date(month.getFullYear(), month.getMonth(), 1);
    const weekStart = firstWeekday();
    const blanks = ((monthStart.getDay() - weekStart) + 7) % 7;
    const length = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate();

    const headings = React.useMemo(() => {
        // 7 January 2024 was a Sunday, so this walks a real week in order.
        const format = new Intl.DateTimeFormat(undefined, {weekday: 'narrow'});

        return Array.from({length: 7}, (_, index) => format.format(new Date(2024, 0, 7 + weekStart + index)));
    }, [weekStart]);

    const monthLabel = new Intl.DateTimeFormat(undefined, {month: 'long', year: 'numeric'}).format(monthStart);
    const dateLabel = new Intl.DateTimeFormat(undefined, {weekday: 'short', day: 'numeric', month: 'short'}).format(day);

    const backAMonth = dayOpen(new Date(month.getFullYear(), month.getMonth(), 0));
    const onAMonth = dayOpen(new Date(month.getFullYear(), month.getMonth() + 1, 1));

    const marks = Array.from({length: 12}, (_, index) => index);
    const hand = step === 'minute' ?
        pointAt(minute / MINUTE_STEP, OUTER) :
        pointAt(hour % 12, hour < 12 ? OUTER : INNER);

    return (
        <div className='vntv-pop'>
            <div className='vntv-pop-head'>
                <button
                    type='button'
                    className='vntv-pop-tab'
                    aria-pressed={step === 'date'}
                    onClick={() => {
                        setMonth(new Date(day.getFullYear(), day.getMonth(), 1));
                        setStep('date');
                    }}
                >{dateLabel}</button>
                <span className='vntv-pop-clock'>
                    <button
                        type='button'
                        className='vntv-pop-tab vntv-pop-num'
                        aria-pressed={step === 'hour'}
                        aria-label={`Hour, ${pad(hour)}`}
                        onClick={() => setStep('hour')}
                    >{pad(hour)}</button>
                    {':'}
                    <button
                        type='button'
                        className='vntv-pop-tab vntv-pop-num'
                        aria-pressed={step === 'minute'}
                        aria-label={`Minutes, ${pad(minute)}`}
                        onClick={() => setStep('minute')}
                    >{pad(minute)}</button>
                </span>
            </div>

            {step === 'date' ? (
                <div className='vntv-cal'>
                    <div className='vntv-cal-head'>
                        <button
                            type='button'
                            className='vntv-cal-step'
                            aria-label='Previous month'
                            disabled={!backAMonth}
                            onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))}
                        >{'‹'}</button>
                        <span className='vntv-cal-month'>{monthLabel}</span>
                        <button
                            type='button'
                            className='vntv-cal-step'
                            aria-label='Next month'
                            disabled={!onAMonth}
                            onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))}
                        >{'›'}</button>
                    </div>
                    <div className='vntv-cal-grid'>
                        {headings.map((letter, index) => (
                            <span
                                key={`h${index}`}
                                className='vntv-cal-letter'
                                aria-hidden={true}
                            >{letter}</span>
                        ))}
                        {Array.from({length: blanks}, (_, index) => (
                            <span key={`b${index}`}/>
                        ))}
                        {Array.from({length}, (_, index) => {
                            const date = new Date(month.getFullYear(), month.getMonth(), index + 1);

                            return (
                                <button
                                    key={index}
                                    type='button'
                                    className='vntv-cal-day'
                                    aria-pressed={date.getTime() === day.getTime()}
                                    disabled={!dayOpen(date)}
                                    onClick={() => pickDay(date)}
                                >{index + 1}</button>
                            );
                        })}
                    </div>
                </div>
            ) : (
                <svg
                    ref={dial}
                    className='vntv-dial'
                    viewBox='0 0 200 200'
                    tabIndex={0}
                    role='slider'
                    aria-label={step === 'hour' ? 'Hour. Arrow keys change it; Enter goes on to the minutes.' : 'Minutes. Arrow keys change them; Enter finishes.'}
                    aria-valuemin={0}
                    aria-valuemax={step === 'hour' ? 23 : 55}
                    aria-valuenow={step === 'hour' ? hour : minute}
                    aria-valuetext={`${pad(hour)}:${pad(minute)}`}
                    onKeyDown={(event) => {
                        if (event.key === 'ArrowUp' || event.key === 'ArrowRight') {
                            event.preventDefault();
                            nudge(1);
                        } else if (event.key === 'ArrowDown' || event.key === 'ArrowLeft') {
                            event.preventDefault();
                            nudge(-1);
                        } else if (event.key === 'Enter' || event.key === ' ') {
                            event.preventDefault();
                            if (step === 'hour') {
                                setStep('minute');
                            } else {
                                onClose();
                            }
                        }
                    }}
                    onPointerDown={(event) => {
                        event.currentTarget.setPointerCapture(event.pointerId);
                        setDragging(true);
                        aimAt(event.clientX, event.clientY);
                    }}
                    onPointerMove={(event) => {
                        if (dragging) {
                            aimAt(event.clientX, event.clientY);
                        }
                    }}
                    onPointerUp={() => {
                        setDragging(false);
                        if (step === 'hour') {
                            setStep('minute');
                        } else {
                            onClose();
                        }
                    }}
                    onPointerCancel={() => setDragging(false)}
                >
                    <circle
                        className='vntv-dial-face'
                        cx={CENTRE}
                        cy={CENTRE}
                        r={94}
                    />
                    <line
                        className='vntv-dial-hand'
                        x1={CENTRE}
                        y1={CENTRE}
                        x2={hand.x}
                        y2={hand.y}
                    />
                    <circle
                        className='vntv-dial-pin'
                        cx={CENTRE}
                        cy={CENTRE}
                        r={3}
                    />
                    <circle
                        className='vntv-dial-knob'
                        cx={hand.x}
                        cy={hand.y}
                        r={KNOB}
                    />

                    {marks.map((index) => {
                        const outer = step === 'minute' ? index * MINUTE_STEP : index;
                        const spot = pointAt(index, OUTER);
                        const open = step === 'minute' ? minuteOpen(outer) : hourOpen(outer);

                        return (
                            <text
                                key={`o${index}`}
                                className='vntv-dial-mark'
                                data-open={open}
                                data-chosen={outer === (step === 'minute' ? minute : hour) ? 'true' : undefined}
                                x={spot.x}
                                y={spot.y}
                            >{pad(outer)}</text>
                        );
                    })}

                    {step === 'hour' && marks.map((index) => {
                        const spot = pointAt(index, INNER);

                        return (
                            <text
                                key={`i${index}`}
                                className='vntv-dial-mark vntv-dial-inner'
                                data-open={hourOpen(index + 12)}
                                data-chosen={index + 12 === hour ? 'true' : undefined}
                                x={spot.x}
                                y={spot.y}
                            >{pad(index + 12)}</text>
                        );
                    })}
                </svg>
            )}

            <div className='vntv-pop-foot'>
                <button
                    type='button'
                    className='vntv-pill'
                    onClick={onClose}
                >{'Done'}</button>
            </div>
        </div>
    );
};
