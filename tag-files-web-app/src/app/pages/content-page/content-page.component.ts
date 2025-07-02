import {Component, effect, inject, signal} from '@angular/core';
import {ActivatedRoute} from '@angular/router';
import {CommonModule} from '@angular/common';
import {MatChipsModule} from '@angular/material/chips';
import {LibraryApiService, LibraryItem} from '../../services/api/library-api.service';
import {VideoPlayerComponent} from '../../components/video-player/video-player.component';

const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

@Component({
  selector: 'app-content-page',
  standalone: true,
  imports: [CommonModule, MatChipsModule, VideoPlayerComponent],
  templateUrl: './content-page.component.html',
  styleUrl: './content-page.component.scss'
})
export class ContentPageComponent {
  protected readonly item = signal<LibraryItem | null>(null);
  private readonly route = inject(ActivatedRoute);
  private readonly libraryApi = inject(LibraryApiService);

  constructor() {
    effect(() => {
      const id = Number(this.route.snapshot.paramMap.get('id'));
      if (id) {
        this.libraryApi.getItem(id).subscribe(item => this.item.set(item));
      }
    });
  }

  protected getVideoOptions() {
    // See options: https://videojs.com/guides/options
    return {
      fill: true,
      autoplay: false,
      muted: false,
      preload: 'auto',
      sources: {
        src: this.getContentUrl(),
        type: "video/mp4",
      },
    };
  }

  protected getThumbnailUrl() {
    return ContentBaseUrl + this.item()!.thumbnailPath;
  }

  protected getContentUrl() {
    return ContentBaseUrl + this.item()!.path;
  }
}
