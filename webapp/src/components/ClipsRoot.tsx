import React from 'react';

import {
    closeClips,
    closeEditor,
    dropClipCaches,
    editorRelinked,
    getClipsOpenings,
    getEditorOpenings,
    refreshDrafts,
    useClipEditor,
    useClipsDialog,
} from '../clips';
import {useAccess, useIdentity} from '../identity';
import {mayWatch} from '../lockLogic';

import {ClipEditor} from './ClipEditor';
import {ClipsModal} from './ClipsModal';

// Where the Clips dialog and the clip editor live: once for
// the whole window, at the root of the app rather than in the panel, so it
// outlives the panel being closed (Mattermost unmounts the side panel) and
// answers every way of opening it — the header's clapper, the panel's row,
// the trophies, a shared clip's card in a channel, a clip link.
//
// Registered in index.tsx (addendum 4.8): in the main window as a Global
// component, which Mattermost renders on every route, including a plugin's
// own page and other products; where that is missing, as a Root component,
// which renders with the channel view only — the dialog's state is
// module-level, so one asked for elsewhere shows when the channel view comes
// back. In a popped-out window, as a Root component, which is the one that
// renders there. The System Console renders neither.

// Hosts mounted, in the order they mounted. Only the first renders: two
// registrations in one window (a Global and a Root on a server that renders
// both, or the lab's own host) must not draw two dialogs over each other.
let hostCount = 0;
const mounted: number[] = [];
const hostListeners = new Set<() => void>();

function hostsChanged(): void {
    Array.from(hostListeners).forEach((listener) => listener());
}

// Mattermost wraps a Global component in an error boundary of its own, which
// prints an error over the whole app. A dialog that fails renders nothing
// instead, and leaves the page and the panel alone. A copy of MiniPlayer's,
// which can also say that it failed.
class Quiet extends React.Component<{children: React.ReactNode; onFail?: () => void}, {failed: boolean}> {
    state = {failed: false};

    static getDerivedStateFromError() {
        return {failed: true};
    }

    componentDidCatch() {
        this.props.onFail?.();
    }

    render() {
        return this.state.failed ? null : this.props.children;
    }
}

export const ClipsRoot: React.FC = () => {
    const [ticket] = React.useState(() => {
        hostCount += 1;

        return hostCount;
    });
    const [, forceRender] = React.useState(0);

    React.useEffect(() => {
        const listener = () => forceRender((value) => value + 1);
        hostListeners.add(listener);
        mounted.push(ticket);
        hostsChanged();

        return () => {
            hostListeners.delete(listener);
            const at = mounted.indexOf(ticket);
            if (at >= 0) {
                mounted.splice(at, 1);
            }
            hostsChanged();
        };
    }, [ticket]);

    if (mounted[0] !== ticket) {
        return null;
    }

    return (
        <Quiet>
            <ClipsHost/>
        </Quiet>
    );
};

// The one host that renders: follows the reader's access, and shows the
// editor or the dialog when one is open — the editor first: it takes the
// dialog's place while it is open, and the dialog comes back after it.
// Nothing at all unless the reader may watch clips (linked, or let in by an
// admin): no dialog and no request while "me" is not known, and nothing
// behind the lock — a clip asked for there waits in the dialog's state until
// the lock opens (design/lock-screen.md 7). What is dropped when access is
// lost is access.ts's to drop.
const ClipsHost: React.FC = () => {
    const me = useIdentity();
    const access = useAccess();
    const watch = mayWatch(access);
    const linked = access === 'linked';
    const state = useClipsDialog();
    const editor = useClipEditor();

    // Somebody else signing in on the same page starts from nothing. A
    // layout effect, so it is done before any effect of the dialog's asks
    // for anything: those run later in the same commit, and an answer asked
    // for before a drop is thrown away.
    const userId = me?.userId || '';
    const previous = React.useRef(userId);
    React.useLayoutEffect(() => {
        const before = previous.current;
        previous.current = userId;
        const someoneElse = before !== '' && userId !== '' && before !== userId;
        if (someoneElse) {
            dropClipCaches();

            // An editor is somebody's own: whoever signs in next does not
            // get it.
            closeEditor(false);
        }
    }, [userId]);

    // Linked, the drafts are fetched: on mount, and again whenever the reader
    // links or changes. An editor that locked when they unlinked goes on.
    React.useEffect(() => {
        if (linked) {
            refreshDrafts();
            editorRelinked();
        }
    }, [userId, linked]);

    if (!watch || !me) {
        return null;
    }

    if (editor) {
        return (
            <Quiet
                key={`editor-${getEditorOpenings()}`}
                onFail={() => closeEditor(false)}
            >
                <ClipEditor
                    state={editor}
                    linked={linked}
                />
            </Quiet>
        );
    }

    if (!state) {
        return null;
    }

    // A dialog that fails to render — a clip the server sent in a shape this
    // build cannot draw — is closed rather than left open and blank, so the
    // clapper does not go on saying it is open and the next press of it
    // opens it again. A boundary of its own, made afresh for each opening,
    // so one failure does not stop the dialog for the rest of the page's
    // life, and the host's own work above (dropping and refetching) goes on.
    return (
        <Quiet
            key={getClipsOpenings()}
            onFail={closeClips}
        >
            <ClipsModal
                state={state}
                linked={linked}
                me={me}
            />
        </Quiet>
    );
};
