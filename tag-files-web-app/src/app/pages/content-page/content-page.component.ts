import {ChangeDetectionStrategy, Component, inject, signal} from '@angular/core';
import {ActivatedRoute} from '@angular/router';
import {CommonModule} from '@angular/common';
import {MatChipsModule} from '@angular/material/chips';
import {LibraryApiService, LibraryItem} from '../../services/api/library-api.service';
import {VideoPlayerComponent} from '../../components/video-player/video-player.component';
import {MatIconModule} from '@angular/material/icon';
import {MatButtonModule} from '@angular/material/button';
import {ImageGridComponent} from '../../components/image-grid/image-grid.component';

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
  private readonly route = inject(ActivatedRoute);
  private readonly libraryApi = inject(LibraryApiService);

  constructor() {
    this.route.paramMap.subscribe(params => {
      const id = Number(params.get('id'));
      if (id) {
        this.item.set(null);
        this.similarItems.set([]);
        this.libraryApi.getItem(id).subscribe(item => this.item.set(item));
        this.libraryApi.getSimilarItems(id).subscribe(items => this.similarItems.set(items));
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
      }
    });
  }

  protected getSimilarItemsPaginatedList() {
    const items = this.similarItems();
    return {
      items,
      totalItems: items.length,
      pageIndex: 0,
      totalPages: 1,
    };
  }
}
