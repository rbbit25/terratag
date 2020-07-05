package main

import (
	"encoding/json"
	. "github.com/env0/terratag/cli"
	"github.com/env0/terratag/convert"
	. "github.com/env0/terratag/errors"
	"github.com/env0/terratag/file"
	. "github.com/env0/terratag/providers"
	"github.com/env0/terratag/tag_keys"
	. "github.com/env0/terratag/terraform"
	. "github.com/env0/terratag/tfschema"
	"github.com/env0/terratag/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"log"
	"strings"
)

func main() {
	Terratag()
}

func Terratag() {
	tags, dir, isSkipTerratagFiles, isMissingArg := InitArgs()

	tfVersion := GetTerraformVersion()

	if isMissingArg || !IsTerraformInitRun(dir) {
		return
	}

	matches := GetTerraformFilePaths(dir)

	tagDirectoryResources(dir, matches, tags, isSkipTerratagFiles, tfVersion)
}

func tagDirectoryResources(dir string, matches []string, tags string, isSkipTerratagFiles bool, tfVersion int) {
	for _, path := range matches {
		if isSkipTerratagFiles && strings.HasSuffix(path, "terratag.tf") {
			log.Print("Skipping file ", path, " as it's already tagged")
		} else {
			tagFileResources(path, dir, tags, tfVersion)
		}
	}
}

func tagFileResources(path string, dir string, tags string, tfVersion int) {
	log.Print("Processing file ", path)
	hcl := file.ReadHCLFile(path)

	terratag := convert.TerratagLocal{
		Found: map[string]hclwrite.Tokens{},
		Added: jsonToHclMap(tags),
	}

	filename := file.GetFilename(path)

	anyTagged := false
	var swappedTagsStrings []string

	for _, topLevelBlock := range hcl.Body().Blocks() {
		if topLevelBlock.Type() == "resource" {
			log.Print("Processing resource ", topLevelBlock.Labels())

			resourceType := GetResourceType(*topLevelBlock)
			tagId := GetTagIdByResource(resourceType, false)

			isTaggable, isTaggableViaSpecialTagBlock := IsTaggable(dir, *topLevelBlock)

			if isTaggable {
				log.Print("Resource taggable, processing...")
				if !isTaggableViaSpecialTagBlock {
					// for now, we count on it that if there's a single "tag" in the schema (unlike "tags" block),
					// then no "tags" interpolation is used, but rather multiple instances of a "tag" block
					// https://www.terraform.io/docs/providers/aws/r/autoscaling_group.html
					swappedTagsStrings = append(swappedTagsStrings, tagBlock(filename, terratag, topLevelBlock, tfVersion, tagId))
				} else {
					convert.AppendTagBlocks(topLevelBlock, tags)
				}
				anyTagged = true
			}

			// handle nested taggable blocks
			nestedBlocks := GetTaggableNestedBlocks(topLevelBlock)
			tagId = GetTagIdByResource(resourceType, true)

			for _, block := range nestedBlocks {
				swappedTagsStrings = append(swappedTagsStrings, tagBlock(filename, terratag, block, tfVersion, tagId))
			}

			if len(nestedBlocks) == 0 && !isTaggable {
				log.Print("Resource not taggable, skipping. ")
			}
		}
	}

	if anyTagged {
		convert.AppendLocalsBlock(hcl, filename, terratag)

		text := string(hcl.Bytes())

		swappedTagsStrings = append(swappedTagsStrings, terratag.Added)
		text = convert.UnquoteTagsAttribute(swappedTagsStrings, text)

		file.ReplaceWithTerratagFile(path, text)
	} else {
		log.Print("No taggable resources found in file ", path, " - skipping")
	}
}

func tagBlock(filename string, terratag convert.TerratagLocal, block *hclwrite.Block, tfVersion int, tagId string) string {
	hasExistingTags := convert.MoveExistingTags(filename, terratag, block, tagId)

	tagsValue := ""
	if hasExistingTags {
		tagsValue = "merge( " + convert.GetExistingTagsExpression(terratag.Found[tag_keys.GetResourceExistingTagsKey(filename, block)]) + ", local." + tag_keys.GetTerratagAddedKey(filename) + ")"
	} else {
		tagsValue = "local." + tag_keys.GetTerratagAddedKey(filename)
	}

	if tfVersion == 11 {
		tagsValue = "${" + tagsValue + "}"
	}

	block.Body().SetAttributeValue(tagId, cty.StringVal(tagsValue))

	return tagsValue
}

func jsonToHclMap(tags string) string {
	var tagsMap map[string]string
	err := json.Unmarshal([]byte(tags), &tagsMap)
	PanicOnError(err, nil)

	keys := utils.SortObjectKeys(tagsMap)

	var mapContent []string
	for _, key := range keys {
		mapContent = append(mapContent, "\""+key+"\"="+"\""+tagsMap[key]+"\"")
	}
	return "{" + strings.Join(mapContent, ",") + "}"
}
