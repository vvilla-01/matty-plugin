import React from 'react';

// A row that opens and closes rather than appearing and vanishing.
//
// The voice strip and the Up next rail both come and go on their own — somebody
// joins Mumble, somebody plans a stream — and both used to do it between one
// frame and the next, shoving everything below them down the panel with no
// warning. Sliding is not decoration here: it is what tells a reader that the
// panel gained a row instead of that they lost their place in it.
//
// Three things this has to get right, and each of them is a way the obvious
// implementation is wrong:
//
//  1. NOT ON FIRST PAINT. Opening the panel should not play an animation for
//     every row that happens to have something in it. The first render is drawn
//     at its final size; the transition is only armed afterwards.
//  2. THE CONTENT STAYS WHILE IT CLOSES. Rendering nothing the moment show goes
//     false collapses an empty box — the row would disappear and then animate.
//     The last content that was shown is kept until it has finished closing.
//  3. IT IS NOT THERE WHEN IT IS CLOSED. A closed row is inert and hidden from
//     screen readers, or the panel keeps a tab stop and a heading nobody can
//     see.
//
// The height comes from grid-template-rows going 0fr to 1fr, which is the one
// way to transition to a height nobody has measured. max-height would need a
// number picked in advance, and a rail of cards has no such number.

// A little longer than the collapse transition in the stylesheet (280ms), so
// the content is only dropped once nobody can see it go.
const CLOSE_MS = 340;

type Props = {
    show: boolean;
    children: React.ReactNode;

    // Named for the log, and for anybody reading the DOM.
    label: string;

    // Kept in the page while it is closed — inert and hidden all the same —
    // for what has to go on running behind it: the footer's clip controls,
    // whose switch may still be waiting on the server when the sheet closes.
    keep?: boolean;

    // A class of its own beside vntv-reveal, for a row that has to sit
    // differently: 3.4.0's Playing now, which gives its gap in the panel back
    // while it is closed (design/3.4.0.md 5.4).
    className?: string;
};

export const Reveal: React.FC<Props> = ({show, children, label, keep = false, className}) => {
    // Armed after the first paint, so nothing slides on load.
    const [armed, setArmed] = React.useState(false);

    // What to draw while closing. Only ever updated while open, so a close
    // animates the row that was there rather than an empty one.
    const kept = React.useRef<React.ReactNode>(null);
    if (show) {
        kept.current = children;
    }

    // And let go of it once it has closed. Kept for ever, a collapsed row went
    // on holding whatever was in it — a form, its half-typed title, a timer
    // ticking every thirty seconds — inert and invisible, until the next time
    // it happened to open.
    const [lingering, setLingering] = React.useState(show);
    React.useEffect(() => {
        if (show) {
            setLingering(true);

            return undefined;
        }

        const timer = window.setTimeout(() => {
            kept.current = null;
            setLingering(false);
        }, CLOSE_MS);

        return () => window.clearTimeout(timer);
    }, [show]);

    const armFrame = React.useRef(0);

    React.useEffect(() => {
        // Two frames: one for the browser to paint the initial state, one to
        // arm. Arming in the same frame as the first paint lets the transition
        // run on it, which is the thing this is avoiding.
        armFrame.current = window.requestAnimationFrame(() => {
            armFrame.current = window.requestAnimationFrame(() => setArmed(true));
        });

        return () => window.cancelAnimationFrame(armFrame.current);
    }, []);

    return (
        <div
            className={className ? `vntv-reveal ${className}` : 'vntv-reveal'}
            data-open={show ? 'true' : 'false'}
            data-armed={armed ? 'true' : undefined}
        >
            <div
                className='vntv-reveal-inner'
                aria-label={label}
                aria-hidden={show ? undefined : true}

                // inert is what actually takes it out of the tab order; the
                // attribute is ignored by browsers that do not have it, which
                // is why aria-hidden is set as well rather than instead.
                {...(show ? {} : {inert: ''})}
            >
                {show || keep ? children : (lingering ? kept.current : null)}
            </div>
        </div>
    );
};
