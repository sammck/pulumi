// Copyright 2016-2017, Pulumi Corporation.  All rights reserved.

import { Log } from "./log";
import { options } from "./settings";

// debugPromiseTimeout can be set to enable promises debugging.  If it is -1, it has no effect.  Be careful setting
// this to other values, because too small a value will cause legitimate promise resolutions to appear as timeouts.
const debugPromiseTimeout: number = -1;

// leakDetectorScheduled is true when the promise leak detector is scheduled for process exit.
let leakDetectorScheduled: boolean = false;
// leakCandidates tracks the list of potential leak candidates.
let leakCandidates: Set<Promise<any>> = new Set<Promise<any>>();
// unhandledHandlerScheduled is true when the unhandled promise detector is scheduled for this process.
let unhandledHandlerScheduled: boolean = false;

function promiseDebugString(p: Promise<any>): string {
    return `CONTEXT: ${(<any>p)._debugCtx}\n` +
        `STACK_TRACE:\n` +
        `${(<any>p)._debugStackTrace}`;
}

// debuggablePromise optionally wraps a promise with some goo to make it easier to debug common problems.
export function debuggablePromise<T>(p: Promise<T>, ctx?: any): Promise<T> {
    // If the unhandled handler isn't active yet, schedule it.
    if (!unhandledHandlerScheduled) {
        process.on("unhandledRejection", (reason, p) => {
            if (!Log.hasErrors()) {
                console.error("Unhandled promise rejection:");
                console.error(reason);
                console.error(reason.stack);
                console.error(promiseDebugString(p));
            }
        });
        unhandledHandlerScheduled = true;
    }

    // Setup leak detection.
    if (!leakDetectorScheduled) {
        process.on('exit', (code: number) => {
            // Only print leaks if we're exiting normally.  Otherwise, it could be a crash, which of
            // course yields things that look like "leaks".
            if (code === 0 && !Log.hasErrors()) {
                for (let leaked of leakCandidates) {
                    console.error("Promise leak detected:");
                    console.error(promiseDebugString(leaked));
                }
            }
        });
        leakDetectorScheduled = true;
    }

    // Whack some stack onto the promise.
    (<any>p)._debugCtx = ctx;
    (<any>p)._debugStackTrace = new Error().stack;

    // Add this promise to the leak candidates list, and schedule it for removal if it resolves.
    leakCandidates.add(p);
    p.then((v: any) => leakCandidates.delete(p), (err: any) => leakCandidates.delete(p));

    // If the timeout is -1, don't register a timer.
    if (debugPromiseTimeout === -1) {
        return p;
    }

    // Create a timer that we race against the original promise.
    let timetok: any;
    let timeout = new Promise<T>((resolve, reject) => {
        timetok = setTimeout(() => {
            clearTimeout(timetok);
            reject(
                `Promise timeout after ${debugPromiseTimeout}ms;\n` +
                `CONTEXT: ${ctx}\n` +
                `STACK TRACE:\n` +
                `${(<any>p)._debugStackTrace}`
            );
        }, debugPromiseTimeout);
    });

    // Ensure to cancel the timer should the promise actually resolve.
    p.then((v: any) => clearTimeout(timetok), (err: any) => clearTimeout(timetok));

    // Now race them; first one wins!
    return Promise.race([ p, timeout ]);
}

// errorString produces a string from an error, conditionally including additional diagnostics.
export function errorString(err: Error): string {
    if (options.includeStacks && err.stack) {
        return err.stack;
    }
    return err.toString();
}

