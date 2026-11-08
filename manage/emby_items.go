package manage

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// FetchMovies returns a page of movies from a library (or from every library
// when libraryID is empty) with the fields the audit needs.
func (c *EmbyClient) FetchMovies(libraryID string, start, limit int) ([]embyItem, int, error) {
	q := url.Values{}
	if libraryID != "" {
		q.Set("ParentId", libraryID)
	}
	q.Set("Recursive", "true")
	q.Set("IncludeItemTypes", "Movie")
	q.Set("Fields", "Genres,Studios,People,Overview,ImageTags,ProviderIds,Path,ProductionYear")
	q.Set("StartIndex", fmt.Sprint(start))
	q.Set("Limit", fmt.Sprint(limit))

	var page struct {
		Items            []embyItem `json:"Items"`
		TotalRecordCount int        `json:"TotalRecordCount"`
	}
	if err := c.do(http.MethodGet, "/Items", q, nil, &page); err != nil {
		return nil, 0, err
	}
	return page.Items, page.TotalRecordCount, nil
}

// RefreshMetadata asks Emby to re-scrape one item.
//
// replaceAll=false only fills gaps: Emby keeps every value that already exists,
// so a stale Japanese title or summary stays. replaceAll=true lets provider
// output win, which is what a full repair needs.
func (c *EmbyClient) RefreshMetadata(itemID string, replaceAll bool) error {
	q := url.Values{}
	q.Set("MetadataRefreshMode", "FullRefresh")
	q.Set("ImageRefreshMode", "Default")
	q.Set("ReplaceAllMetadata", fmt.Sprint(replaceAll))
	q.Set("ReplaceAllImages", "false")
	q.Set("Recursive", "false")
	return c.do(http.MethodPost, "/Items/"+url.PathEscape(itemID)+"/Refresh", q, nil, nil)
}

// itemDTO loads a full item DTO.
//
// Emby only returns the full DTO (the shape POST /Items/{id} accepts) through
// the user-scoped endpoint; /Items?Ids= returns a partial object that the
// update endpoint rejects with "Value cannot be null".
func (c *EmbyClient) itemDTO(itemID string) (map[string]any, error) {
	if uid := c.resolveUserID(); uid != "" {
		var dto map[string]any
		err := c.do(http.MethodGet,
			"/Users/"+url.PathEscape(uid)+"/Items/"+url.PathEscape(itemID), nil, nil, &dto)
		if err == nil && len(dto) > 0 {
			return dto, nil
		}
	}

	var page struct {
		Items []map[string]any `json:"Items"`
	}
	q := url.Values{}
	q.Set("Ids", itemID)
	q.Set("Fields", "Name,Overview,SortName")
	if err := c.do(http.MethodGet, "/Items", q, nil, &page); err != nil {
		return nil, err
	}
	if len(page.Items) == 0 {
		return nil, fmt.Errorf("item %s not found", itemID)
	}
	return page.Items[0], nil
}

// RenameItem changes an item's display name; Emby applies the change live.
func (c *EmbyClient) RenameItem(itemID, name string) error {
	dto, err := c.itemDTO(itemID)
	if err != nil {
		return err
	}
	dto["Name"] = name
	dto["SortName"] = name
	return c.do(http.MethodPost, "/Items/"+url.PathEscape(itemID), nil, dto, nil)
}

// FolderRef is one folder entry shown in the console's folder picker.
type FolderRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// LibraryLocations maps library ids to their configured root paths.
func (c *EmbyClient) LibraryLocations() map[string][]string {
	out := map[string][]string{}
	var folders []struct {
		ItemId    string   `json:"ItemId"`
		Locations []string `json:"Locations"`
	}
	if err := c.do(http.MethodGet, "/Library/VirtualFolders", nil, nil, &folders); err != nil {
		return out
	}
	for _, f := range folders {
		out[f.ItemId] = f.Locations
	}
	return out
}

// WipeMetadata clears every metadata field of one item except its images, so a
// following refresh rebuilds the entry. The file path and image tags stay.
func (c *EmbyClient) WipeMetadata(itemID string) error {
	dto, err := c.itemDTO(itemID)
	if err != nil {
		return err
	}
	code := ""
	if ids, ok := dto["ProviderIds"].(map[string]any); ok {
		for k, v := range ids {
			if strings.EqualFold(k, "MetaTube") {
				if s, _ := v.(string); s != "" {
					parts := strings.SplitN(s, ":", 2)
					code = strings.ToUpper(parts[len(parts)-1])
				}
			}
		}
	}
	if code == "" {
		if name, _ := dto["Name"].(string); name != "" {
			code = strings.ToUpper(strings.Fields(name)[0])
		}
	}
	if code == "" {
		return fmt.Errorf("无法确定番号")
	}
	dto["Name"] = ""
	dto["SortName"] = ""
	dto["ForcedSortName"] = ""
	dto["OriginalTitle"] = ""
	dto["Overview"] = ""
	dto["Taglines"] = []any{}
	dto["Genres"] = []any{}
	dto["GenreItems"] = []any{}
	dto["Tags"] = []any{}
	dto["TagItems"] = []any{}
	dto["Studios"] = []any{}
	dto["StudioItems"] = []any{}
	dto["People"] = []any{}
	dto["OfficialRating"] = ""
	dto["CustomRating"] = ""
	dto["ProductionYear"] = 0
	dto["PremiereDate"] = nil
	return c.do(http.MethodPost, "/Items/"+url.PathEscape(itemID), nil, dto, nil)
}

// AddGenre adds one genre to an item, keeping the existing ones.
//
// Emby stores genres twice: the "Genres" string list and the "GenreItems" object
// list. Both must be updated or the item's genre filter does not see the value.
func (c *EmbyClient) AddGenre(itemID, genre string) error {
	dto, err := c.itemDTO(itemID)
	if err != nil {
		return err
	}
	names := make([]string, 0, 8)
	seen := map[string]struct{}{}
	if raw, ok := dto["Genres"].([]any); ok {
		for _, v := range raw {
			if s, _ := v.(string); strings.TrimSpace(s) != "" {
				key := strings.ToLower(strings.TrimSpace(s))
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				names = append(names, strings.TrimSpace(s))
			}
		}
	}
	if strings.TrimSpace(genre) != "" {
		key := strings.ToLower(strings.TrimSpace(genre))
		if _, dup := seen[key]; !dup {
			names = append(names, strings.TrimSpace(genre))
		}
	}

	genres := make([]any, 0, len(names))
	items := make([]any, 0, len(names))
	for _, n := range names {
		genres = append(genres, n)
		items = append(items, map[string]any{"Name": n})
	}
	dto["Genres"] = genres
	dto["GenreItems"] = items
	return c.do(http.MethodPost, "/Items/"+url.PathEscape(itemID), nil, dto, nil)
}
