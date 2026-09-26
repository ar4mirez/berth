package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ar4mirez/berth/internal/assets"
)

// Released images (#41). A berth release embeds the digest of the multi-arch image its release
// workflow built from the same image/ and pushed to GHCR (signed with cosign). berth pulls that
// digest (the digest, from a verified binary, is the integrity check) and tags it as its usual
// local name, berth/claude-env:<content hash>, instead of building for minutes. When the pull
// fails (offline, say) berth builds locally, as before. Development builds embed no digest and
// behave exactly as before.

// publishedImage is the image this berth pulls: $BERTH_PUBLISHED_IMAGE if set ("none" turns
// pulling off), else the release's embedded digest. Only for berth's own embedded image/ (not
// BERTH_IMAGE_DIR, not a legacy checkout's compose.yml).
func (a *App) publishedImage() string {
	src := assets.PublishedImage
	if v := a.Getenv("BERTH_PUBLISHED_IMAGE"); v != "" {
		src = v
	}
	if src == "none" || a.Getenv("BERTH_IMAGE_DIR") != "" {
		return ""
	}
	return src
}

// imageFromRelease makes sure berth's image tag exists by pulling the published image, if there is
// one. It reports whether the tag is there now; false means the caller builds, as before (and when
// a pull failed, it says so).
func (a *App) imageFromRelease(ctx context.Context) bool {
	src := a.publishedImage()
	if src == "" {
		return false
	}
	set, own, err := a.composeAssets()
	if err != nil || !own {
		return false
	}
	if a.quietRun(ctx, "docker", "image", "inspect", set.Tag) == nil {
		return true
	}
	fmt.Fprintf(a.Stderr, "%s: pulling the released image %s\n", Tool, src)
	if err := a.passthrough(ctx, false, "docker", "pull", src); err != nil {
		fmt.Fprintf(a.Stderr, "%s: couldn't pull it; building the image locally instead\n", Tool)
		return false
	}
	if err := a.quietRun(ctx, "docker", "tag", src, set.Tag); err != nil {
		fmt.Fprintf(a.Stderr, "%s: couldn't tag it as %s; building the image locally instead\n", Tool, set.Tag)
		return false
	}
	return true
}

// Pull is `berth pull`: get the released image now (nothing restarts), so the orgs' next restarts
// don't have to wait for it.
func (a *App) Pull(ctx context.Context) error {
	if err := a.State.Writable("pull the image"); err != nil {
		return err
	}
	if a.publishedImage() == "" {
		return errors.New("this berth has no released image (a development build, or BERTH_PUBLISHED_IMAGE=none): use berth build")
	}
	set, _, err := a.composeAssets()
	if err != nil {
		return err
	}
	if !a.imageFromRelease(ctx) {
		return fmt.Errorf("couldn't pull %s", a.publishedImage())
	}
	fmt.Fprintf(a.Stdout, "%s is ready (from %s). Nothing was restarted.\n", set.Tag, a.publishedImage())
	return nil
}
