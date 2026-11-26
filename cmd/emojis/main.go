// Command emojis uploads the ChastiSafe card icons as Discord application
// emoji. Application emoji belong to the bot application, not to a guild, so
// they render in DMs and in every server the bot is installed in.
//
// The bot resolves emoji IDs at runtime, so this command never edits source.
// Run it from the repository root so it can read .env:
//
//	go run ./cmd/emojis
//	mise run emojis
package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
	"golang.org/x/image/draw"

	"locksmith/internal/config"
)

const (
	// emojiModule is the module that ships the card icons.
	emojiModule = "github.com/zastraeaxyz/chastisafe-go"
	// emojiAssets is the icon directory inside that module.
	emojiAssets = "assets/cards/png"

	emojiSize     = 128
	maxEmojiBytes = 256 * 1024
)

var (
	assetsFlag  = flag.String("assets", "", "directory of card PNGs (default: the chastisafe-go module assets)")
	dryRunFlag  = flag.Bool("dry-run", false, "show what would change without calling Discord")
	replaceFlag = flag.Bool("replace", false, "delete and recreate an existing emoji so its image is updated")
	pruneFlag   = flag.Bool("prune", false, "delete application emoji that have no matching asset")
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "emojis:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	dir, err := assetDir(*assetsFlag)
	if err != nil {
		return err
	}
	files, err := assetFiles(dir)
	if err != nil {
		return err
	}

	session, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return fmt.Errorf("creating discord session: %w", err)
	}

	me, err := session.User("@me")
	if err != nil {
		return fmt.Errorf("validating bot token: %w", err)
	}
	fmt.Printf("application %s (%s)\n", me.Username, me.ID)

	existing, err := session.ApplicationEmojis(me.ID)
	if err != nil {
		return fmt.Errorf("listing application emoji: %w", err)
	}
	byName := make(map[string]*discordgo.Emoji, len(existing))
	for _, emoji := range existing {
		if emoji != nil && emoji.Name != "" {
			byName[emoji.Name] = emoji
		}
	}

	wanted := make(map[string]bool, len(files))
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".png")
		if err := validEmojiName(name); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		wanted[name] = true

		current := byName[name]
		if current != nil && !*replaceFlag {
			fmt.Printf("skip    %-16s already exists\n", name)
			continue
		}

		data, err := emojiPNG(file)
		if err != nil {
			return err
		}

		if current != nil {
			if *dryRunFlag {
				fmt.Printf("replace %-16s %d bytes\n", name, len(data))
				continue
			}
			if err := session.ApplicationEmojiDelete(me.ID, current.ID); err != nil {
				return fmt.Errorf("deleting emoji %q: %w", name, err)
			}
			fmt.Printf("delete  %-16s %s\n", name, current.ID)
		}

		if *dryRunFlag {
			fmt.Printf("create  %-16s %d bytes\n", name, len(data))
			continue
		}

		created, err := session.ApplicationEmojiCreate(me.ID, &discordgo.EmojiParams{
			Name:  name,
			Image: "data:image/png;base64," + base64.StdEncoding.EncodeToString(data),
		})
		if err != nil {
			return fmt.Errorf("creating emoji %q: %w", name, err)
		}
		fmt.Printf("create  %-16s %s\n", name, created.ID)
	}

	if *pruneFlag {
		if err := prune(session, me.ID, byName, wanted); err != nil {
			return err
		}
	}

	if *dryRunFlag {
		fmt.Println("dry run: no changes made")
	}
	return nil
}

// prune deletes application emoji that no longer have an asset.
func prune(session *discordgo.Session, appID string, existing map[string]*discordgo.Emoji, wanted map[string]bool) error {
	names := make([]string, 0, len(existing))
	for name := range existing {
		if !wanted[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if *dryRunFlag {
			fmt.Printf("prune   %-16s %s\n", name, existing[name].ID)
			continue
		}
		if err := session.ApplicationEmojiDelete(appID, existing[name].ID); err != nil {
			return fmt.Errorf("pruning emoji %q: %w", name, err)
		}
		fmt.Printf("prune   %-16s %s\n", name, existing[name].ID)
	}
	return nil
}

// assetDir returns the directory of card icons. The default is the assets
// folder of the chastisafe-go module the bot is built with.
func assetDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}

	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", emojiModule).Output()
	if err != nil {
		return "", fmt.Errorf("locating %s (use -assets): %w", emojiModule, err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", fmt.Errorf("%s is not downloaded; use -assets", emojiModule)
	}
	return filepath.Join(dir, emojiAssets), nil
}

// assetFiles returns the card PNGs sorted by name.
func assetFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading assets: %w", err)
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".png" {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no PNG assets in %s", dir)
	}
	sort.Slice(files, func(i, j int) bool { return files[i] < files[j] })
	return files, nil
}

// validEmojiName enforces Discord's emoji name rules.
func validEmojiName(name string) error {
	if len(name) < 2 || len(name) > 32 {
		return errors.New("emoji name must be 2 to 32 characters")
	}
	for _, r := range name {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return errors.New("emoji name may only contain a-z, 0-9, and _")
	}
	return nil
}

// emojiPNG reads an icon, fits it into a 128x128 transparent square, and
// returns a PNG under Discord's 256 KiB limit.
func emojiPNG(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer file.Close()

	src, err := png.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", filepath.Base(path), err)
	}

	canvas := image.NewNRGBA(image.Rect(0, 0, emojiSize, emojiSize))
	box := src.Bounds()
	width, height := fit(box.Dx(), box.Dy(), emojiSize)
	offsetX := (emojiSize - width) / 2
	offsetY := (emojiSize - height) / 2
	draw.CatmullRom.Scale(
		canvas,
		image.Rect(offsetX, offsetY, offsetX+width, offsetY+height),
		src,
		box,
		draw.Src,
		nil,
	)

	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&buf, canvas); err != nil {
		return nil, fmt.Errorf("encoding %s: %w", filepath.Base(path), err)
	}
	if buf.Len() > maxEmojiBytes {
		return nil, fmt.Errorf("%s is %d bytes after resizing, over Discord's %d byte limit",
			filepath.Base(path), buf.Len(), maxEmojiBytes)
	}
	return buf.Bytes(), nil
}

// fit scales width x height to fit inside limit, keeping the aspect ratio.
func fit(width, height, limit int) (int, int) {
	if width <= 0 || height <= 0 {
		return limit, limit
	}
	if width >= height {
		return limit, max(1, height*limit/width)
	}
	return max(1, width*limit/height), limit
}
