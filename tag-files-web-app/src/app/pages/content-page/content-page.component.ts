import {ChangeDetectionStrategy, Component, inject, signal} from '@angular/core';
import {ActivatedRoute} from '@angular/router';
import {CommonModule} from '@angular/common';
import {MatChipsModule} from '@angular/material/chips';
import {LibraryApiService, LibraryItem} from '../../services/api/library-api.service';
import {VideoPlayerComponent} from '../../components/video-player/video-player.component';
import {MatIconModule} from '@angular/material/icon';
import {MatButtonModule} from '@angular/material/button';
import {ImageGridComponent} from '../../components/image-grid/image-grid.component';
import {SearchService} from '../../services/search.service';
import {LibraryItemEditModalComponent} from '../library-item-edit-modal/library-item-edit-modal.component';
import {MatDialog} from '@angular/material/dialog';
import {
  LibraryCollectionsApiService,
  LibraryCollectionWithItems
} from '../../services/api/library-collections-api.service';

const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

@Component({
  selector: 'app-content-page',
  standalone: true,
  imports: [CommonModule, MatChipsModule, VideoPlayerComponent, MatIconModule, MatButtonModule, ImageGridComponent],
  templateUrl: './content-page.component.html',
  styleUrl: './content-page.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ContentPageComponent {
  protected readonly item = signal<LibraryItem | null>(null);
  protected readonly similarItems = signal<LibraryItem[]>([]);
  protected readonly collection = signal<LibraryCollectionWithItems | null>(null);
  private readonly route = inject(ActivatedRoute);
  private readonly libraryApi = inject(LibraryApiService);
  private readonly libraryCollectionsApi = inject(LibraryCollectionsApiService);
  private readonly searchService = inject(SearchService);
  private dialog = inject(MatDialog);

  constructor() {
    this.route.paramMap.subscribe(params => {
      const id = Number(params.get('id'));
      if (id) {
        this.loadItem(id);
      }
    });
  }

  protected getVideoOptions() {
    // See options: https://videojs.com/guides/options
    return {
      fill: true,
      autoplay: true,
      muted: false,
      preload: 'auto',
      sources: {
        src: this.getContentUrl(),
        type: "video/mp4",
      },
    };
  }

  protected getContentUrl() {
    return ContentBaseUrl + this.item()!.path;
  }

  protected toggleFavorite() {
    const item = this.item()!;
    this.libraryApi.toggleFavorite(item.id).subscribe({
      next: () => {
        this.item.set({...item, isFavorite: !item.isFavorite});
        this.searchService.search();
      }
    });
  }

  protected edit() {
    const itemId = this.item()!.id;
    const dialogRef = this.dialog.open(LibraryItemEditModalComponent, {
      data: {
        item: this.item()!,
      },
      width: '800px',
      height: '600px',
    });

    dialogRef.afterClosed().subscribe((result) => {
      if (result === true) {
        this.loadItem(itemId);
        this.searchService.search();
      }
    });
  }

  protected makeItemsPaginatedList(items: LibraryItem[]) {
    return {
      items,
      totalItems: items.length,
      pageIndex: 0,
      totalPages: 1,
    };
  }

  protected loadItem(id: number) {
    this.item.set(null);
    this.similarItems.set([]);
    this.collection.set(null);

    this.libraryApi.getItem(id).subscribe(item => {
      this.item.set(item);
      if (item.collectionId) {
        this.libraryCollectionsApi.getCollection(item.collectionId).subscribe(collection => {
          this.collection.set({
            ...collection,
            items: collection.items.filter(collectionItem => collectionItem.id !== item.id)
          });
        });
      }
    });

    this.libraryApi.getSimilarItems(id).subscribe(items => this.similarItems.set(items));
  }
}
