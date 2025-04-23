import { Injectable, signal } from '@angular/core';

@Injectable({
  providedIn: 'root'
})
export class AppStateService {
  getIsSidebarOpen() {
    return this.isSidebarOpen()
  }

  getGalleryThumbnailSize() {
    return this.galleryThumbnailSize()
  }

  toggleSidebar() {
    this.isSidebarOpen.update(prev => !prev);
  }


  increaseMainGridColumns() {
    this.galleryThumbnailSize.update(prev => prev < 5 ? prev + 1 : prev);
  }

  decreaseMainGridColumns() {
    this.galleryThumbnailSize.update(prev => prev > 1 ? prev - 1 : prev);
  }

  private readonly isSidebarOpen = signal<boolean>(true);
  private readonly galleryThumbnailSize = signal<number>(3);
}
