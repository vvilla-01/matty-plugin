// The panel in a window of its own: Mattermost's "open in a new window" for a
// side-panel plugin.
//
// In the main window the side panel is laid over the page at the page's own
// height, and the panel fills it. In the popped-out window it is in the flow
// of the page instead — Mattermost's #root grid there is at LEAST as tall as
// the window, not exactly as tall — so the panel is as tall as its contents.
// Widen the window and the television, which keeps to 16:9, grows taller,
// until the whole page runs past the bottom of the window and is cut off
// there: the footer, with the stream title and the link, first.
// (components/rhs_popout, components/popout_controller and sass/base/
// _structure.scss, master.)
//
// So in a popped-out window the panel is given the window's height, less
// whatever sits above and below it — the side panel's own header, and
// anything else on the page, such as another plugin's — and its middle
// scrolls, as it does in the side panel, with the footer always at the bottom
// of the window.
//
// Only Mattermost's popped-out SIDE PANEL, .rhs-popout. Every popped-out
// window has body.popout, including a popped-out channel, and there the panel
// sits in an ordinary side panel beside the channel, sized as it is in the
// main window.

// Never squeezed shorter than this, whatever the window: below it the
// footer and the header are all there would be.
const MIN_HEIGHT = 240;

// Whether this page is Mattermost's popped-out side panel.
export function isPopoutWindow(): boolean {
    return document.querySelector('.rhs-popout') !== null;
}

function inPopout(panel: HTMLElement): boolean {
    return panel.closest('.rhs-popout') !== null;
}

// For a React effect, with the panel's own element.
export function fitPopoutWindow(panel: HTMLElement | null): () => void {
    if (!panel || !inPopout(panel)) {
        return () => undefined;
    }

    const root = document.getElementById('root') || document.body;

    const fit = () => {
        // What is above the panel, and what is below the box it sits in —
        // not the page's height less the panel's, which once the window is
        // taller than the page counts the page's own stretch to fill the
        // window as something else's room, and so could shrink the panel but
        // never let it grow back.
        const here = panel.getBoundingClientRect();
        const page = root.getBoundingClientRect();
        const box = (panel.parentElement || root).getBoundingClientRect();
        const others = (here.top - page.top) + Math.max(0, page.bottom - box.bottom);
        const height = Math.max(MIN_HEIGHT, Math.floor(window.innerHeight - others));

        if (panel.style.height !== `${height}px`) {
            panel.style.height = `${height}px`;
        }
    };

    fit();
    window.addEventListener('resize', fit);

    // Something that comes or goes above or below the panel changes the room
    // it has without the window changing size.
    let observer: ResizeObserver | null = null;
    if (typeof ResizeObserver !== 'undefined') {
        observer = new ResizeObserver(() => fit());
        observer.observe(root);
    }

    return () => {
        window.removeEventListener('resize', fit);
        observer?.disconnect();
        panel.style.removeProperty('height');
    };
}
