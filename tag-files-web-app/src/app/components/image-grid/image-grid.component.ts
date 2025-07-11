import {ChangeDetectionStrategy, Component, computed, inject, input} from '@angular/core';
import {MatGridListModule} from '@angular/material/grid-list';
import {AppStateService} from '../../services/app-state.service';
import {LightgalleryModule} from 'lightgallery/angular';
import {LibraryApiService, LibraryItem, LibraryItemPaginatedList} from '../../services/api/library-api.service';
import {MatTooltipModule} from '@angular/material/tooltip';
import {MatIconModule} from '@angular/material/icon';
import {NgClass} from '@angular/common';
import {RouterLink} from '@angular/router';

const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

@Component({
  selector: 'app-image-grid',
  imports: [MatGridListModule, LightgalleryModule, MatTooltipModule, MatIconModule, NgClass, RouterLink],
  templateUrl: './image-grid.component.html',
  styleUrl: './image-grid.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ImageGridComponent {
  itemsList = input.required<LibraryItemPaginatedList>();
  private readonly appStateService = inject(AppStateService);
  protected readonly getGridColumns = computed(() => {
    const max = 7;
    const level = this.appStateService.getGalleryThumbnailSize();
    return 4 + (max - level);
  })
  private readonly libraryApi = inject(LibraryApiService);

  toggleFavorite(item: LibraryItem) {
    this.libraryApi.toggleFavorite(item.id).subscribe({
      next: () => item.isFavorite = !item.isFavorite
    });
  }

  protected getFullThumbnailPath(thumbnailPath?: string): string {
    if (!thumbnailPath) {
      return '#';
    }

    return ContentBaseUrl + thumbnailPath;
  }

  protected getTagsTooltip(item: LibraryItem): string {
    if (!item.tags || item.tags.length === 0) {
      return 'No tags';
    }
    return item.tags.join(', ');
  }
}
