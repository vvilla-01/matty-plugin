import React from 'react';

import {useName} from '../name';

import {BrandIcon} from './BrandIcon';

// The right-hand sidebar's title as a stock plugin draws one: the server's
// icon as a small picture, as the App Bar shows it, and what this server
// calls the plugin as plain words in Mattermost's own title style, inherited
// from the header. Drawn again when the name changes (name.ts).
export const Wordmark: React.FC = () => {
    const name = useName();

    return (
        <span className='vntv-wordmark'>
            <BrandIcon
                className='vntv-wordmark-emblem'
                size={20}
                alt=''
            />
            <span className='vntv-wordmark-name'>{name}</span>
        </span>
    );
};
