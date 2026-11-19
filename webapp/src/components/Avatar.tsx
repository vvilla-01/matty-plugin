import React from 'react';

type Props = {
    userId?: string;

    // A picture of its own, used instead of the account's: a 24/7 channel has
    // no account.
    src?: string;

    // Who the picture is of. Doubles as the alt text and tooltip, so a face
    // that cannot be seen still says whose it is.
    name: string;

    className?: string;

    // True where the name is already written beside the picture, so a screen
    // reader does not say it twice.
    decorative?: boolean;
};

// A person's profile picture, from Mattermost's own avatar endpoint — the same
// origin as everything else the panel loads, so no request leaves the server.
//
// Initials when there is no picture to be had: a stream with no linked account
// behind it, or an image that will not load. A broken-image icon in a row of
// faces says something is wrong, which it is not. Size and shape are the
// caller's, through className.
export const Avatar: React.FC<Props> = ({userId, src, name, className, decorative}) => {
    const [failed, setFailed] = React.useState(false);

    const url = src || (userId ? `/api/v4/users/${encodeURIComponent(userId)}/image` : '');

    // A different person, or a new picture, in the same slot gets a fresh try.
    React.useEffect(() => setFailed(false), [url]);

    const classes = `vntv-face${className ? ` ${className}` : ''}`;

    if (!url || failed) {
        return (
            <span
                className={`${classes} vntv-face-initials`}
                role={decorative ? undefined : 'img'}
                aria-label={decorative ? undefined : name}
                aria-hidden={decorative ? true : undefined}
                title={decorative ? undefined : name}
            >{initialsOf(name)}</span>
        );
    }

    return (
        <img
            className={classes}
            src={url}
            alt={decorative ? '' : name}
            title={decorative ? undefined : name}
            loading='lazy'
            draggable={false}
            onError={() => setFailed(true)}
        />
    );
};

function initialsOf(name: string): string {
    const words = name.replace(/^@/, '').split(/[\s._-]+/).filter(Boolean);
    const initials = words.slice(0, 2).map((word) => Array.from(word)[0]).join('').toUpperCase();

    return initials || '?';
}
