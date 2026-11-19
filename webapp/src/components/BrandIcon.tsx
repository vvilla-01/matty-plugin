import React from 'react';

import {APP_BAR_ICON} from '../icon';

// The server's icon as a small picture: the side panel's title, the channel
// header's button and the small player's Back button. One picture
// everywhere, the one the server serves (icon.ts).
//
// A picture that fails to load is an empty space of the same size: nothing
// broken shows. alt is said either way: the space is then an image with that
// name.
type Props = {
    size: number;
    alt: string;
    className?: string;
};

export const BrandIcon: React.FC<Props> = ({size, alt, className}) => {
    const src = APP_BAR_ICON;
    const [failed, setFailed] = React.useState('');

    if (failed === src) {
        return (
            <span
                className={className}
                style={{display: 'inline-block', width: size, height: size}}
                role={alt === '' ? undefined : 'img'}
                aria-label={alt === '' ? undefined : alt}
                aria-hidden={alt === '' ? true : undefined}
            />
        );
    }

    return (
        <img
            className={className}
            src={src}
            alt={alt}
            width={size}
            height={size}
            draggable={false}
            onError={() => setFailed(src)}
        />
    );
};
