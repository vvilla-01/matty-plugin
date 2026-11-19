import React from 'react';
import {createPortal} from 'react-dom';

import {closeHashtag, useOpenHashtag} from '../hashtagOpen';
import {findHashtags} from '../titleParts';
import {jumpToPost} from '../mattermost';
import {searchInMattermost, searchTag} from '../tagSearch';
import type {TagHit} from '../tagSearch';
import {postedAt, postedWhen, viewerZone} from '../time';

import {Avatar} from './Avatar';
import {CloseIcon, HashIcon} from './icons';

// The messages tagged with a hashtag from a stream title, over the panel.
//
// Built like the trophy's Trophies card, and for the same reason: a
// portal onto document.body, because the panel's own boxes clip anything
// opened inside them. Over the panel rather than in place of it, so whatever
// is playing keeps playing while the results are read. Mattermost's own
// search is one button away at the bottom, for anybody who wants it there
// instead — that one does take the side panel, and says so.
//
// Every message is shown as text, never as HTML: it is somebody else's
// words, and Mattermost's own formatter makes hashtags into links that only
// work inside its own post view. Markdown shows as typed.

// Element ids for the dialog's heading and subtitle, one pair per dialog
// opened, from a counter; see TitleSearch.
let dialogs = 0;

// What "Open in Mattermost search" says where there is no search bar to open:
// Mattermost's phone-sized layout, or a popped-out channel.
const NO_SEARCH_BAR = 'Mattermost’s search isn’t on screen in this window, so it can’t be opened from here.';

type Results = {
    status: 'loading' | 'ready' | 'error';
    hits: TagHit[];
    error: string;

    // The last page asked for, whether another could find more, and how
    // asking for it went.
    page: number;
    more: boolean;
    loadingMore: boolean;
    moreError: string;

    // The next page is the dialog's own ask rather than a press of "Load
    // more", and shows nothing while it is on its way; see load.
    quiet: boolean;

    // "Load more" was pressed and found nothing new: the list has reached
    // its end, and says so where the button was.
    ended: boolean;
};

const SEARCHING: Results = {status: 'loading', hits: [], error: '', page: 0, more: false, loadingMore: false, moreError: '', quiet: false, ended: false};

// The first thing in the results that can take focus: the first message, or
// whatever says why there are none — the "Searching…" line, the line saying
// nothing is tagged, or "Try again".
const FIRST_IN_RESULTS = '.vntv-tag-body a[href], .vntv-tag-body button, .vntv-tag-body [tabindex="-1"]';

// More of a message than three lines could ever show. A post can be sixteen
// thousand characters, and the rest would only be laid out to be hidden.
const MESSAGE_CHARS = 1200;

// A message's text with the hashtag marked wherever it is in it — found by the
// same rule that found the message, and compared case-insensitively, as the
// server compares it, so "#Arma3" is marked in a search for #arma3 but the
// "#arma" of "#arma3" and a "#arma3" inside an address are not. Blank lines
// are closed up: three lines of a message are all there is room for.
const MessageText: React.FC<{text: string; tag: string}> = ({text, tag}) => {
    const shown = text.slice(0, MESSAGE_CHARS).trim().replace(/\n\s*\n/g, '\n');
    if (shown === '') {
        return <span className='vntv-tag-text vntv-tag-empty'>{'No text'}</span>;
    }

    const wanted = tag.toUpperCase();
    const marks = findHashtags(shown).filter((found) => found.tag.toUpperCase() === wanted);

    const nodes: React.ReactNode[] = [];
    let at = 0;
    marks.forEach((mark) => {
        if (mark.start > at) {
            nodes.push(shown.slice(at, mark.start));
        }
        nodes.push(
            <mark
                key={mark.start}
                className='vntv-tag-mark'
            >{shown.slice(mark.start, mark.end)}</mark>,
        );
        at = mark.end;
    });
    if (at < shown.length) {
        nodes.push(shown.slice(at));
    }

    return <span className='vntv-tag-text'>{nodes}</span>;
};

type HitProps = {
    hit: TagHit;
    tag: string;
    zone: string | undefined;
    onJump: (hit: TagHit) => void;
};

// One message: whose, where, when, and the message. The whole row is its
// permalink. A plain click goes there in Mattermost, as its own Jump does, and
// leaves the panel open — except on a phone-sized screen, where the panel
// covers the channel and Jump closes it first (jumpToPost); a click with Ctrl,
// Cmd or Shift, or with the middle button, is the browser's, and opens a new
// tab or window as it would for any link.
const Hit: React.FC<HitProps> = ({hit, tag, zone, onJump}) => {
    // Where it was posted, and whether it is a reply — which, with Collapsed
    // Reply Threads on, is opened at the start of its thread; see searchTag.
    let reply = '';
    if (hit.reply) {
        reply = hit.inThread ? 'Reply · opens at the start of its thread' : 'Reply in a thread';
    }
    const where = [hit.where, reply].filter(Boolean).join(' · ');

    const body = (
        <>
            <Avatar
                className='vntv-tag-face'
                userId={hit.authorId}
                name={hit.author}
                decorative={true}
            />
            <span className='vntv-tag-main'>
                <span className='vntv-tag-meta'>
                    <span className='vntv-tag-author'>{hit.author}</span>
                    {hit.at > 0 && (
                        <time
                            className='vntv-tag-when'
                            dateTime={new Date(hit.at).toISOString()}
                            title={postedAt(hit.at, zone)}
                        >{postedWhen(hit.at, zone)}</time>
                    )}
                </span>
                {where !== '' && <span className='vntv-tag-where'>{where}</span>}
                <MessageText
                    text={hit.message}
                    tag={tag}
                />
            </span>
        </>
    );

    if (hit.path === '') {
        return <div className='vntv-tag-hit'>{body}</div>;
    }

    return (
        <a
            className='vntv-tag-hit'
            data-hit={hit.id}
            href={hit.href}
            onClick={(event) => {
                if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
                    return;
                }

                event.preventDefault();
                onJump(hit);
            }}
        >{body}</a>
    );
};

type Props = {
    tag: string;
    onClose: () => void;
};

export const HashtagSearch: React.FC<Props> = ({tag, onClose}) => {
    const card = React.useRef<HTMLDivElement>(null);
    const closer = React.useRef<HTMLButtonElement>(null);
    const [ids] = React.useState(() => {
        dialogs += 1;

        return {title: `villanet-tag-title-${dialogs}`, sub: `villanet-tag-sub-${dialogs}`};
    });
    const zone = React.useMemo(() => viewerZone(), []);

    // Whether closing puts focus back on the hashtag that opened this. Not
    // after a jump to a message or into Mattermost's search, which have
    // somewhere of their own to put it.
    const giveFocusBack = React.useRef(true);

    // Read through a ref, so a new onClose from a re-render does not re-run
    // the effect below — which would take focus from the results back to the
    // close button every time the panel updated.
    const closeRef = React.useRef(onClose);
    closeRef.current = onClose;

    const alive = React.useRef(true);

    React.useEffect(() => {
        const previous = document.activeElement as HTMLElement | null;
        closer.current?.focus();

        const onKey = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                event.stopPropagation();
                closeRef.current();

                return;
            }

            // Tab goes round the dialog rather than out of it into a page
            // that cannot be used while this is over it.
            const box = card.current;
            if (event.key !== 'Tab' || !box) {
                return;
            }

            const stops = Array.from(box.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), [tabindex="0"]'));
            if (stops.length === 0) {
                return;
            }

            const first = stops[0];
            const last = stops[stops.length - 1];
            const active = document.activeElement;
            if (!box.contains(active)) {
                event.preventDefault();
                (event.shiftKey ? last : first).focus();
            } else if (event.shiftKey && active === first) {
                event.preventDefault();
                last.focus();
            } else if (!event.shiftKey && active === last) {
                event.preventDefault();
                first.focus();
            }
        };

        document.addEventListener('keydown', onKey, true);

        return () => {
            alive.current = false;
            document.removeEventListener('keydown', onKey, true);
            if (giveFocusBack.current && previous?.isConnected) {
                previous.focus?.();
            }
        };
    }, []);

    const [results, setResults] = React.useState<Results>(SEARCHING);

    // One request at a time. A newer one — a retry, the next page — cancels
    // whatever is still on its way, and closing cancels it too; an answer to
    // a cancelled request is never used.
    const request = React.useRef<AbortController | null>(null);

    // The results as last drawn, for adding a page to.
    const shown = React.useRef<TagHit[]>([]);
    shown.current = results.hits;

    // Where keyboard focus goes once the results are redrawn, when the button
    // that had it has been replaced or has gone — so it never falls out of the
    // dialog onto the page behind, from where the next Tab starts again at
    // Close, twenty messages away from where it was.
    //
    //   - "Try again" is replaced by the search starting over: focus waits on
    //     the "Searching…" line, then goes to the first message, or the line
    //     saying there are none, or "Try again" if it failed again.
    //   - "Load more" stays put while it loads, as it is never disabled, and
    //     then hands focus to the first message it added, or to the line
    //     saying that was all. If it failed it is still there to press again,
    //     and keeps focus.
    //
    // A selector, read after the redraw; kept while the search is still on its
    // way, so focus is held on the "Searching…" line rather than nowhere.
    const focusAfter = React.useRef<string | null>(null);

    // pressed: asked for with "Load more" or "Try again" rather than by the
    // dialog itself.
    const load = React.useCallback((page: number, pressed = false) => {
        request.current?.abort();
        const controller = new AbortController();
        request.current = controller;

        setResults((was) => (page === 0 ? SEARCHING : {...was, loadingMore: true, moreError: '', quiet: !pressed, ended: false}));

        searchTag(tag, page, controller.signal).then((found) => {
            if (controller.signal.aborted) {
                return;
            }

            // A page can repeat a message the one before it ended on, if
            // somebody tagged a new one in between; each is listed once.
            const before = page === 0 ? [] : shown.current;
            const seen = new Set(before.map((hit) => hit.id));
            const fresh = found.hits.filter((hit) => !seen.has(hit.id));
            const ended = pressed && page > 0 && fresh.length === 0 && !found.more;

            const onButton = document.activeElement instanceof HTMLElement && document.activeElement.classList.contains('vntv-tag-more');
            if (page > 0 && onButton) {
                if (fresh.length > 0) {
                    focusAfter.current = `[data-hit="${CSS.escape(fresh[0].id)}"]`;
                } else if (ended) {
                    focusAfter.current = '.vntv-tag-end';
                }
            }

            setResults({
                status: 'ready',
                hits: before.concat(fresh),
                error: '',
                page,
                more: found.more,
                loadingMore: false,
                moreError: '',
                quiet: false,
                ended,
            });

            // After the first page, at once, the second, as Mattermost's own
            // search does (components/search_results/search_results.tsx): only
            // an empty page says there is no more (see TagPage), and asking
            // now means "Load more" is only offered once a second page has
            // found something — a database search, which sends everything on
            // the first page, never shows it at all.
            if (page === 0 && found.more) {
                load(1);
            }
        }).catch((reason) => {
            if (controller.signal.aborted) {
                return;
            }

            const message = reason instanceof Error && reason.message ? reason.message : 'Could not search just now.';
            setResults((was) => (page === 0 ? {...SEARCHING, status: 'error', error: message} : {...was, loadingMore: false, moreError: message}));
        });
    }, [tag]);

    React.useEffect(() => {
        load(0);

        return () => request.current?.abort();
    }, [load]);

    React.useEffect(() => {
        const selector = focusAfter.current;
        if (selector === null) {
            return;
        }

        card.current?.querySelector<HTMLElement>(selector)?.focus();
        if (results.status !== 'loading') {
            focusAfter.current = null;
        }
    }, [results]);

    const jump = (hit: TagHit) => {
        giveFocusBack.current = false;
        onClose();
        jumpToPost(hit.path, hit.href);
    };

    const [handOff, setHandOff] = React.useState<{busy: boolean; note: string}>({busy: false, note: ''});

    // Not disabled while it waits, for the same reason as "Load more": a
    // disabled button drops the focus it had.
    const openInMattermost = () => {
        if (handOff.busy) {
            return;
        }

        setHandOff({busy: true, note: ''});
        searchInMattermost(tag).then((outcome) => {
            if (!alive.current) {
                return;
            }

            if (outcome === 'opened') {
                giveFocusBack.current = false;
                onClose();

                return;
            }

            setHandOff({
                busy: false,
                note: outcome === 'unavailable' ? NO_SEARCH_BAR : `The Mattermost window this panel came from didn’t answer. Search for #${tag} there.`,
            });
        });
    };

    const term = `#${tag}`;

    let moreLabel = 'Load more';
    if (results.loadingMore) {
        moreLabel = 'Loading…';
    } else if (results.moreError !== '') {
        moreLabel = 'Try loading more again';
    }

    return createPortal(
        <div
            className='vntv vntv-modal'
            role='presentation'
            onMouseDown={(event) => {
                // Only a press that both starts and ends on the backdrop.
                // Otherwise a text selection dragged out of the card closes it.
                if (event.target === event.currentTarget) {
                    onClose();
                }
            }}
        >
            <div
                ref={card}
                className='vntv-modal-card vntv-tag-card'
                role='dialog'
                aria-modal={true}
                aria-labelledby={ids.title}
                aria-describedby={ids.sub}
                onMouseDown={(event) => event.stopPropagation()}
            >
                <header className='vntv-modal-head'>
                    <span
                        className='vntv-modal-icon'
                        aria-hidden={true}
                    ><HashIcon size={20}/></span>
                    <span className='vntv-tag-heading'>
                        <h2
                            id={ids.title}
                            className='vntv-modal-title vntv-tag-title'
                        >{term}</h2>
                        <p
                            id={ids.sub}
                            className='vntv-tag-sub'
                        >{`Messages tagged ${term}, newest first`}</p>
                    </span>
                    <button
                        ref={closer}
                        type='button'
                        className='vntv-modal-close'
                        aria-label='Close'
                        onClick={onClose}
                    ><CloseIcon size={16}/></button>
                </header>

                <div
                    className='vntv-modal-body vntv-tag-body'
                    aria-busy={results.status === 'loading' || results.loadingMore}
                >
                    {results.status === 'loading' && (
                        <p
                            className='vntv-tag-status'
                            role='status'
                            tabIndex={-1}
                        >{`Searching messages for ${term}…`}</p>
                    )}

                    {results.status === 'error' && (
                        <div
                            className='vntv-tag-status vntv-tag-error'
                            role='alert'
                        >
                            <p>{results.error}</p>
                            <button
                                type='button'
                                className='vntv-pill'
                                onClick={(event) => {
                                    if (document.activeElement === event.currentTarget) {
                                        focusAfter.current = FIRST_IN_RESULTS;
                                    }
                                    load(0, true);
                                }}
                            >{'Try again'}</button>
                        </div>
                    )}

                    {results.status === 'ready' && results.hits.length === 0 && (
                        <p
                            className='vntv-tag-status'
                            role='status'
                            tabIndex={-1}
                        >{`No messages tagged ${term} yet.`}</p>
                    )}

                    {results.hits.length > 0 && (
                        <ol className='vntv-tag-list'>
                            {results.hits.map((hit) => (
                                <li
                                    key={hit.id}
                                    className='vntv-tag-item'
                                >
                                    <Hit
                                        hit={hit}
                                        tag={tag}
                                        zone={zone}
                                        onJump={jump}
                                    />
                                </li>
                            ))}
                        </ol>
                    )}

                    {results.status === 'ready' && results.hits.length > 0 && (results.more || results.moreError !== '') && !(results.loadingMore && results.quiet) && (
                        <div className='vntv-tag-more-row'>
                            {results.moreError !== '' && (
                                <p
                                    className='vntv-tag-note'
                                    role='alert'
                                >{results.moreError}</p>
                            )}

                            {/* Not disabled while it loads: a disabled button
                                drops the keyboard focus it had; see focusAfter. */}
                            <button
                                type='button'
                                className='vntv-tag-more'
                                aria-disabled={results.loadingMore ? true : undefined}
                                onClick={() => {
                                    if (!results.loadingMore) {
                                        load(results.page + 1, true);
                                    }
                                }}
                            >{moreLabel}</button>
                        </div>
                    )}

                    {results.ended && (
                        <div className='vntv-tag-more-row'>
                            <p
                                className='vntv-tag-note vntv-tag-end'
                                role='status'
                                tabIndex={-1}
                            >{`That’s every message tagged ${term}.`}</p>
                        </div>
                    )}
                </div>

                <footer className='vntv-tag-foot'>
                    <button
                        type='button'
                        className='vntv-pill vntv-tag-open'
                        aria-disabled={handOff.busy ? true : undefined}
                        onClick={openInMattermost}
                    >{'Open in Mattermost search'}</button>
                    {handOff.note !== '' && (
                        <p
                            className='vntv-tag-note'
                            role='status'
                        >{handOff.note}</p>
                    )}
                </footer>
            </div>
        </div>,
        document.body,
    );
};

// Mounted once, in the panel. The dialog lasts as long as the panel does:
// a panel closed with the dialog open takes it with it, and does not bring
// it back the next time it opens.
export const HashtagSearchHost: React.FC = () => {
    const tag = useOpenHashtag();

    React.useEffect(() => () => closeHashtag(), []);

    return tag === null ? null : (
        <HashtagSearch
            key={tag}
            tag={tag}
            onClose={closeHashtag}
        />
    );
};
