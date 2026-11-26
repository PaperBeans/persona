// The composer's slot for the files picked for a message (#8361; the tiles themselves are FileTilesBody.tsx). Most
// messages have none, so the tiles' code is off the first screen and loads the first time a message has a file. Drawn
// only once the chunk is in, never suspending: a Suspense here would hold the composer's other updates (the text being
// typed) while it loads. A chunk that will not load is reported, and the slot's next mount asks again.
import { useEffect, useState } from "react";
import type { ComposerFile } from "../../lib/composer-files";
import { reportBoundary } from "../../lib/error-reports";
import { lazyPage } from "../../lib/lazy-page";
import { isChunkError } from "../PageBoundary";

const FileTilesBody = lazyPage(() => import("./FileTilesBody").then((m) => m.FileTiles));

export function FileTiles(props: { draftKey: string; files: ComposerFile[] }) {
  const [ready, setReady] = useState(FileTilesBody.ready);
  const some = props.files.length > 0;
  useEffect(() => {
    if (ready || !some) return;
    let live = true;
    FileTilesBody.preload().then(
      () => live && setReady(true),
      (error: unknown) => reportBoundary(error, "composer_files", isChunkError(error)),
    );
    return () => {
      live = false;
    };
  }, [ready, some]);
  return ready ? <FileTilesBody {...props} /> : null; // the tiles draw nothing for no file
}
