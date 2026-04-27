// Minimal .tsx fixture to exercise the tsx grammar path. The analyzer
// picks the tsx grammar based on the extension; this file uses JSX that
// the plain typescript grammar rejects, so a successful parse here proves
// the grammar routing works.

import * as React from "react";

export interface HelloProps {
    name: string;
}

export function Hello(props: HelloProps): JSX.Element {
    if (props.name.length > 0) {
        return <div className="greeting">Hello, {props.name}!</div>;
    }
    return <span>No name.</span>;
}

export const Count = (props: { n: number }) => {
    return <span>{props.n}</span>;
};
