package images

import (
	"fmt"

	"github.com/containers/podman/v5/cmd/podman/common"
	"github.com/containers/podman/v5/cmd/podman/inspect"
	"github.com/containers/podman/v5/cmd/podman/registry"
	"github.com/containers/podman/v5/cmd/podman/utils"
	"github.com/containers/podman/v5/pkg/domain/entities"
	inspectTypes "github.com/containers/podman/v5/pkg/inspect"
	"github.com/spf13/cobra"
)

var (
	// Command: podman image _inspect_
	inspectCmd = &cobra.Command{
		Use:               "inspect [options] IMAGE [IMAGE...]",
		Short:             "Display the configuration of an image",
		Long:              `Displays the low-level information of an image identified by name or ID.`,
		RunE:              inspectExec,
		ValidArgsFunction: common.AutocompleteImages,
		Example: `podman image inspect alpine
  podman image inspect --format "imageId: {{.Id}} size: {{.Size}}" alpine
  podman image inspect --format "image: {{.ImageName}} driver: {{.Driver}}" myctr`,
	}
	inspectOpts *entities.InspectOptions
)

func init() {
	registry.Commands = append(registry.Commands, registry.CliCommand{
		Command: inspectCmd,
		Parent:  imageCmd,
	})
	inspectOpts = new(entities.InspectOptions)
	flags := inspectCmd.Flags()

	formatFlagName := "format"
	flags.StringVarP(&inspectOpts.Format, formatFlagName, "f", "json", "Format the output to a Go template or json")
	_ = inspectCmd.RegisterFlagCompletionFunc(formatFlagName, common.AutocompleteFormat(&inspectTypes.ImageData{}))

	// Add digest flag
	flags.StringVar(&inspectOpts.DigestType, "digest", "", "digest type to use (sha256 or sha512)")
	_ = inspectCmd.RegisterFlagCompletionFunc("digest", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"sha256", "sha512"}, cobra.ShellCompDirectiveNoFileComp
	})
}

func inspectExec(cmd *cobra.Command, args []string) error {
	if inspectOpts.DigestType != "" {
		if inspectOpts.DigestType != "sha256" && inspectOpts.DigestType != "sha512" {
			return fmt.Errorf("invalid digest type: %s (must be sha256 or sha512)", inspectOpts.DigestType)
		}
		_, cleanup, err := utils.OverrideStorageConfWithDigest(inspectOpts.DigestType)
		if err != nil {
			return err
		}
		defer cleanup()
	}
	inspectOpts.Type = common.ImageType
	return inspect.Inspect(args, *inspectOpts)
}
