package ui

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/domain"
)

func TestStickerCatalogBuildsSpecialAndPackTabs(t *testing.T) {
	m := NewRootModel(nil, 50, false)
	m.stickerPicker = &stickerPicker{gen: 4, loading: true}
	recent := domain.StickerRef{Document: domain.DocumentRef{ID: 10}}
	favorite := domain.StickerRef{Document: domain.DocumentRef{ID: 20}}

	m, _ = m.handleStickerCatalogLoaded(stickerCatalogLoadedMsg{
		gen: 4,
		catalog: domain.StickerCatalog{
			Recent:    []domain.StickerRef{recent},
			Favorites: []domain.StickerRef{favorite},
			Packs: []domain.StickerPackRef{{
				ID: 30, AccessHash: 40, Title: "Cats", ShortName: "cats",
			}},
		},
	})

	require.False(t, m.stickerPicker.loading)
	require.Len(t, m.stickerPicker.tabs, 3)
	require.Equal(t, "Recent", m.stickerPicker.tabs[0].title)
	require.Equal(t, "Favorites", m.stickerPicker.tabs[1].title)
	require.Equal(t, "Cats", m.stickerPicker.tabs[2].title)
	require.True(t, m.stickerPicker.tabs[0].loaded)
	require.True(t, m.stickerPicker.tabs[1].loaded)
	require.False(t, m.stickerPicker.tabs[2].loaded, "ordinary packs are loaded only when selected")
	require.Equal(t, int64(10), m.stickerPicker.selectedID())
}

func TestStickerCatalogIgnoresStaleLoad(t *testing.T) {
	m := NewRootModel(nil, 50, false)
	m.stickerPicker = &stickerPicker{gen: 5, loading: true}

	m, _ = m.handleStickerCatalogLoaded(stickerCatalogLoadedMsg{
		gen:     4,
		catalog: domain.StickerCatalog{Recent: []domain.StickerRef{{Document: domain.DocumentRef{ID: 10}}}},
	})

	require.True(t, m.stickerPicker.loading)
	require.Empty(t, m.stickerPicker.tabs)
}
