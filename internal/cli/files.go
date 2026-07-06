package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/AnirudhSinghRajora/DFMS/internal/dfmsclient"
	"github.com/AnirudhSinghRajora/DFMS/pkg/models"
)

// defaultMultipartThreshold is the file size above which uploads switch to the
// chunked multipart protocol unless overridden by config (defaults.multipart_threshold).
const defaultMultipartThreshold int64 = 64 << 20 // 64 MiB

// newFilesCommand builds the `files` command group for file operations against
// the active context.
func newFilesCommand(env *appEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "files",
		Aliases: []string{"file"},
		Short:   "Upload, download, list, and manage files",
	}
	cmd.AddCommand(
		newFilesUploadCommand(env),
		newFilesListCommand(env),
		newFilesGetCommand(env),
		newFilesDownloadCommand(env),
		newFilesDeleteCommand(env),
		newFilesSearchCommand(env),
		newFilesVersionsCommand(env),
	)
	return cmd
}

func newFilesUploadCommand(env *appEnv) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "upload <path>",
		Short: "Upload a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()

			info, err := f.Stat()
			if err != nil {
				return err
			}
			if info.IsDir() {
				return fmt.Errorf("%q is a directory, not a file", path)
			}

			displayName := name
			if displayName == "" {
				displayName = filepath.Base(path)
			}

			sess, err := env.session()
			if err != nil {
				return err
			}

			// Show upload progress on an interactive terminal; off a TTY (or with
			// --quiet/--no-progress/-o json) the meter is a no-op passthrough.
			meter := env.progressMeter(cmd, "Uploading "+displayName, info.Size())
			src := meter.reader(f)

			// Large files use the chunked multipart protocol so memory stays
			// bounded and a failure mid-upload is cleaned up server-side.
			var result *dfmsclient.UploadResult
			if info.Size() > sess.multipartThreshold {
				result, err = sess.client.UploadInParts(cmd.Context(), displayName, src, dfmsclient.DefaultPartSize)
			} else {
				result, err = sess.client.UploadFile(cmd.Context(), displayName, src)
			}
			meter.Finish()
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if handled, herr := writeStructured(out, env.opts.output, result); handled || herr != nil {
				return herr
			}
			if env.opts.quiet {
				fmt.Fprintln(out, result.ID)
				return nil
			}
			renderUploadResult(out, result)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name to store the file under (default: the file's base name)")
	return cmd
}

func newFilesListCommand(env *appEnv) *cobra.Command {
	var (
		page     int
		pageSize int
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List your files",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sess, err := env.session()
			if err != nil {
				return err
			}
			list, err := sess.client.ListFiles(cmd.Context(), page, pageSize)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if handled, herr := writeStructured(out, env.opts.output, list); handled || herr != nil {
				return herr
			}
			if env.opts.quiet {
				for i := range list.Files {
					fmt.Fprintln(out, list.Files[i].ID)
				}
				return nil
			}
			renderFileList(out, env.style, list)
			return nil
		},
	}
	cmd.Flags().IntVar(&page, "page", 1, "page number")
	cmd.Flags().IntVar(&pageSize, "page-size", 20, "items per page (max 100)")
	return cmd
}

func newFilesGetCommand(env *appEnv) *cobra.Command {
	return &cobra.Command{
		Use:               "get <id>",
		Short:             "Show metadata for a file",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeFileIDs(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, err := env.session()
			if err != nil {
				return err
			}
			file, err := sess.client.GetFile(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if handled, herr := writeStructured(out, env.opts.output, file); handled || herr != nil {
				return herr
			}
			renderFileDetail(out, file)
			return nil
		},
	}
}

func newFilesDownloadCommand(env *appEnv) *cobra.Command {
	var outFile string
	cmd := &cobra.Command{
		Use:               "download <id>",
		Short:             "Download a file's contents",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeFileIDs(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			sess, err := env.session()
			if err != nil {
				return err
			}

			if outFile == "-" {
				_, err = sess.client.DownloadFile(cmd.Context(), id, cmd.OutOrStdout())
				return err
			}

			// Otherwise stream to a temp file in the destination directory and
			// rename it into place, so a failed download never leaves a partial
			// file at the final path.
			destDir := "."
			if outFile != "" {
				destDir = filepath.Dir(outFile)
			}
			tmp, err := os.CreateTemp(destDir, ".dfms-download-*")
			if err != nil {
				return fmt.Errorf("creating temp file: %w", err)
			}
			tmpName := tmp.Name()

			// Total size is unknown until the response arrives, so the meter
			// shows transferred bytes and rate rather than a percentage bar.
			meter := env.progressMeter(cmd, "Downloading", 0)
			info, derr := sess.client.DownloadFile(cmd.Context(), id, meter.writer(tmp))
			meter.Finish()
			closeErr := tmp.Close()
			if derr != nil {
				_ = os.Remove(tmpName)
				return derr
			}
			if closeErr != nil {
				_ = os.Remove(tmpName)
				return fmt.Errorf("writing download: %w", closeErr)
			}

			dest := outFile
			if dest == "" {
				name := info.Filename
				if name == "" {
					name = id
				}
				dest = filepath.Base(name) // sanitize a server-provided name
			}
			if err := os.Rename(tmpName, dest); err != nil {
				_ = os.Remove(tmpName)
				return fmt.Errorf("saving download: %w", err)
			}

			if !env.opts.quiet {
				env.successf(cmd.OutOrStdout(), "Downloaded %s (%s)", dest, formatBytes(info.Size))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&outFile, "output-file", "O", "",
		`destination path, or "-" for stdout (default: the server-provided name)`)
	return cmd
}

func newFilesDeleteCommand(env *appEnv) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:               "delete <id>",
		Aliases:           []string{"rm"},
		Short:             "Delete a file",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeFileIDs(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			// Confirm interactively unless --yes is given or input isn't a TTY
			// (so scripts are never blocked on a prompt).
			if !yes && term.IsTerminal(int(os.Stdin.Fd())) {
				confirmed, err := confirm(cmd, fmt.Sprintf("Delete file %q?", id))
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
					return nil
				}
			}

			sess, err := env.session()
			if err != nil {
				return err
			}
			if err := sess.client.DeleteFile(cmd.Context(), id); err != nil {
				return err
			}
			env.successf(cmd.OutOrStdout(), "Deleted file %q", id)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// ── Rendering ────────────────────────────────────────────────

func renderUploadResult(w io.Writer, r *dfmsclient.UploadResult) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "ID:\t%s\n", r.ID)
	fmt.Fprintf(tw, "Name:\t%s\n", r.Name)
	fmt.Fprintf(tw, "Size:\t%s\n", formatBytes(r.Size))
	fmt.Fprintf(tw, "Version:\t%d\n", r.Version)
	fmt.Fprintf(tw, "Chunks:\t%d (%d new, %d deduplicated)\n", r.ChunkCount, r.NewChunks, r.DedupChunks)
	if r.Checksum != "" {
		fmt.Fprintf(tw, "Checksum:\t%s\n", r.Checksum)
	}
	_ = tw.Flush()
}

func renderFileList(w io.Writer, st *styler, list *dfmsclient.FileList) {
	if len(list.Files) == 0 {
		fmt.Fprintln(w, "No files.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, st.HeaderCell("ID\tNAME\tSIZE\tVERSION\tMODIFIED"))
	for i := range list.Files {
		f := &list.Files[i]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
			f.ID, f.Name, formatBytes(f.Size), f.Version, f.UpdatedAt.Local().Format(time.RFC3339))
	}
	_ = tw.Flush()
	if list.TotalPages > 0 {
		fmt.Fprintf(w, "\nPage %d of %d (%d total)\n", list.Page, list.TotalPages, list.Total)
	}
}

func renderFileDetail(w io.Writer, f *models.File) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "ID:\t%s\n", f.ID)
	fmt.Fprintf(tw, "Name:\t%s\n", f.Name)
	fmt.Fprintf(tw, "Size:\t%s\n", formatBytes(f.Size))
	fmt.Fprintf(tw, "Version:\t%d\n", f.Version)
	fmt.Fprintf(tw, "Status:\t%s\n", f.Status)
	fmt.Fprintf(tw, "MIME type:\t%s\n", deref(f.MimeType))
	fmt.Fprintf(tw, "Checksum:\t%s\n", deref(f.Checksum))
	fmt.Fprintf(tw, "Created:\t%s\n", f.CreatedAt.Local().Format(time.RFC3339))
	fmt.Fprintf(tw, "Modified:\t%s\n", f.UpdatedAt.Local().Format(time.RFC3339))
	_ = tw.Flush()
}

// ── Helpers ──────────────────────────────────────────────────

// confirm asks a yes/no question on stderr and reads the answer from the
// command's input. Anything other than "y"/"yes" is treated as no.
func confirm(cmd *cobra.Command, prompt string) (bool, error) {
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N]: ", prompt)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("reading confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// formatBytes renders a byte count in human-readable units (e.g. "1.5 MiB").
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
