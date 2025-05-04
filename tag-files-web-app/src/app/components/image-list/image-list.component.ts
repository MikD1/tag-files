import {Component, inject, input} from '@angular/core';
import {LightgalleryModule} from 'lightgallery/angular';
import {MatTableModule} from '@angular/material/table';
import {MatChipsModule} from '@angular/material/chips';
import {LightGallerySettings} from 'lightgallery/lg-settings';
import {FileType, LibraryItem, LibraryItemPaginatedList} from '../../services/api/library-api.service';
import {Router} from '@angular/router';
import {MatIconModule} from '@angular/material/icon';
import {MatButtonModule} from '@angular/material/button';
import {DatePipe} from '@angular/common';


const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

@Component({
  selector: 'app-image-list',
  imports: [LightgalleryModule, MatTableModule, MatChipsModule, MatIconModule, MatButtonModule, DatePipe],
  templateUrl: './image-list.component.html',
  styleUrl: './image-list.component.scss'
})
export class ImageListComponent {
  gallerySettings = input.required<LightGallerySettings>();
  itemsList = input.required<LibraryItemPaginatedList>();
  protected router = inject(Router);
  protected fileTypes = FileType;
  protected displayedColumns = ['image', 'tags', 'uploadedOn', 'actions'];

  protected getFullThumbnailPath(thumbnailPath?: string): string {
    if (!thumbnailPath) {
      return '#';
    }

    return ContentBaseUrl + thumbnailPath;
  }

  protected getFullContentPath(contentPath: string): string {
    return ContentBaseUrl + contentPath;
  }

  protected getVideoData(item: LibraryItem): string {
    return JSON.stringify({
      source: [
        {
          src: `${this.getFullContentPath(item.path)}`,
          type: item.mediaType,
        }
      ],
      attributes: {
        preload: false,
        playsinline: true,
        controls: true
      }
    });
  }

  protected editItem(id: number) {
    this.router.navigate(['/library/edit', id]);
  }
}
