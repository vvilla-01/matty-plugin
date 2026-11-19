import React from 'react';

import {clapperView} from '../clipLogic';
import {openClips, useClipsDialog, useClipsUnseen, useDrafts, waitingDrafts} from '../clips';
import {useSnapshot} from '../store';
import {useAccess, useIdentity} from '../identity';
import {mayWatch} from '../lockLogic';
import {useBoards} from '../useBoards';

import {LeaderboardModal} from './LeaderboardModal';
import {ClapperIcon, TrophyIcon} from './icons';
import {Wordmark} from './Wordmark';

// The right-hand sidebar's own header row, which Mattermost renders from the
// node handed to registerRightHandSidebarComponent.
//
// The wordmark sits at the left of it and the buttons at the far right, hard
// against Mattermost's own expand and close buttons. That is what the flex
// properties below are for: Mattermost's .sidebar--right__title is itself a
// flex container with `flex: 1 1 auto`, so a node that grows to fill it and
// spaces its own children apart puts the buttons at the right-hand end of the
// header without a single absolute position. The clapper and the trophy are
// one group, so that spacing the children apart does not leave the clapper
// stranded in the middle of the header.
//
// The trophy is only there when there is something to show on any of the
// Trophies card's tabs: either streamers' board, the medals of months that have
// ended, either watchers' board, or a Best Clipper. The medals count on their
// own, because the boards can be empty while they are not — an import can
// bring back past months without the all-time totals — and a champion's
// record should not vanish from the menu with them. A Snoopers board that is
// switched on with nobody on it is not something to show by itself: the card
// would open on "nobody has watched yet" and nothing else.
//
// The clapper opens Clips (clipLogic.ts clapperView says when it is there):
// with the reader's drafts counted on it and a dot for clips they have not
// seen; for somebody let in to watch only, with no count of drafts.
//
// For a locked reader — and while it is not yet known who is reading — the
// header is the wordmark alone: no clapper and no trophy, so there are no
// Trophies behind the lock either (design/lock-screen.md 7).
export const SidebarTitle: React.FC = () => {
    const snapshot = useSnapshot();
    const boards = useBoards();
    const me = useIdentity();
    const access = useAccess();
    const watch = mayWatch(access);
    const [open, setOpen] = React.useState(false);
    const drafts = waitingDrafts(useDrafts(), Date.now());
    const unseen = useClipsUnseen(snapshot.clips);
    const clipsOpen = useClipsDialog() !== null;

    // The Trophies card closes when the lock comes back, and does not open
    // again by itself when it goes.
    React.useEffect(() => {
        if (!watch) {
            setOpen(false);
        }
    }, [watch]);

    // The same function on every render: the card re-renders with every
    // snapshot, and must not treat each one as being opened again.
    const close = React.useCallback(() => setOpen(false), []);

    // From the boards as this page holds them: read when it started, and
    // again whenever the snapshot says they have changed, so the trophy
    // appears with the first row there is to show (boards.ts).
    const anything = watch && ((boards.allTime || []).length > 0 ||
        (boards.month || []).length > 0 ||
        (boards.monthMedals || []).length > 0 ||
        (boards.watchAllTime || []).length > 0 ||
        (boards.watchMonth || []).length > 0 ||
        Boolean(boards.bestClipper) ||

        // 3.2.0 (design/3.2.0.md 3.6, 5.7): anybody's achievement. Until
        // 3.2.1, biggest fans being on was reason enough for a linked reader,
        // so the You tab's opt-out could always be reached; there is none
        // now (design/3.2.1.md 2).
        (boards.achievements || []).length > 0);

    const clapper = clapperView({
        info: snapshot.clips,
        access,
        drafts: access === 'linked' ? drafts.length : 0,
        unseen,
    });

    return (
        <span className='vntv vntv-rhs-title'>
            <Wordmark/>

            {anything || clapper ? (
                <span className='vntv-rhs-actions'>
                    {clapper && (
                        <button
                            type='button'
                            className='vntv-trophy vntv-clapper'
                            aria-label={clapper.label}
                            title={clapper.label}
                            aria-haspopup='dialog'
                            aria-expanded={clipsOpen}
                            onClick={() => openClips()}
                        >
                            <ClapperIcon size={19}/>

                            {/* Said in the button's name, so not read twice. */}
                            {clapper.drafts > 0 ? (
                                <span
                                    className='vntv-clips-count'
                                    aria-hidden={true}
                                >{clapper.drafts}</span>
                            ) : null}
                            {clapper.dot ? (
                                <span
                                    className='vntv-clips-new'
                                    aria-hidden={true}
                                />
                            ) : null}
                        </button>
                    )}

                    {anything && (
                        <button
                            type='button'
                            className='vntv-trophy'
                            aria-label='Trophies'
                            title='Trophies'
                            aria-haspopup='dialog'
                            aria-expanded={open}
                            onClick={() => setOpen(true)}
                        ><TrophyIcon size={19}/></button>
                    )}
                </span>
            ) : null}

            {open && watch && (
                <LeaderboardModal
                    snapshot={snapshot}
                    meUserId={me?.userId}
                    onClose={close}
                />
            )}
        </span>
    );
};
