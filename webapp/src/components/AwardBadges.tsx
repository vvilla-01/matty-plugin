import React from 'react';

import {besideName} from '../awardLogic';
import {useSnapshot} from '../store';
import {voiceLabel, voiceOf} from '../voice';

import {AwardBeside} from './AwardMark';
import {VoiceBadge} from './badges';

type Props = {
    userId?: string;
    size?: number;

    // Adds the ordering rule that pulls the badges in front of the post
    // timestamp. Only meaningful inside Mattermost's post header.
    inPost?: boolean;
};

// Someone's awards, wherever their name appears — and, in front of them,
// headphones while they are on Mumble. That goes first for the same reason
// the live row heads the profile: what somebody is doing now outranks what
// they did in March.
//
// Which awards is theirs to choose (design/3.2.0.md 3.2), and it is decided
// in one place, on the server (awards_decide.go), for every badge beside a
// name: here, on the Live now card and beside "Clipped by", in this client
// and in the app alike. Never having chosen is Automatic, which is exactly
// 3.1.0: the crown, this month's calendar, the Top Snooper's binoculars and
// the Best Clipper's clapperboard, whenever held, in that order, and nothing
// new by itself. A choice is up to four of everything they hold — a past
// month's medal, a Biggest fan heart, an achievement's coin too — in their
// order; one not held now is skipped and kept. Four is the most 3.1.0 ever
// drew, so no post header gets wider.
//
// The headphones are not part of the choice and not counted in the four: they
// say where somebody is now, not what they won.
//
// The Best Clipper is everybody's to see, linked or not: it is an award, like
// the crowns, naming a person and never a clip. The snapshot carries it only
// while Clips are switched on, so it goes when they do.
//
// Read straight out of the snapshot the panel already receives, where each
// person's entry says what goes by their name, so no extra request is needed
// and nothing is worked out here. Renders nothing at all for the
// overwhelming majority of people, which is the point of an award.
//
// Scoped under .vntv like everything else, with the panel shell undone by
// .vntv-inline, because this is rendered inside Mattermost's own furniture
// rather than inside the sidebar.
export const AwardBadges: React.FC<Props> = ({userId, size = 16, inPost}) => {
    const snapshot = useSnapshot();

    if (!userId) {
        return null;
    }

    const voice = voiceOf(snapshot, userId);
    const shown = besideName(snapshot, userId);

    if (!voice && shown.length === 0) {
        return null;
    }

    return (
        <span className={`vntv vntv-inline vntv-awards${inPost ? ' vntv-awards-post' : ''}`}>
            {voice ? (
                <span
                    role='img'
                    aria-label={voiceLabel(voice)}
                    title={voiceLabel(voice)}
                >
                    <VoiceBadge
                        muted={voice.muted}
                        size={size}
                    />
                </span>
            ) : null}
            {shown.map((award) => (
                <AwardBeside
                    key={award.id}
                    award={award}
                    size={size}
                />
            ))}
        </span>
    );
};

type PostProps = {
    post?: {user_id?: string};
};

// Mattermost renders this in the header of every post, beside the name and the
// timestamp. It is given the post and nothing else, so the user id is the hook
// back into the boards.
//
// Note it is suppressed by Mattermost itself on consecutive posts from the same
// person — the ones where it hides the timestamp too — so a run of messages
// shows the award once at the top rather than on every line. That is the
// behaviour of the hook, and it happens to be the right amount of it.
export const PostAwardBadges: React.FC<PostProps> = ({post}) => (
    <AwardBadges
        userId={post?.user_id}
        inPost={true}
    />
);
