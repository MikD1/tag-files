import {Injectable, signal} from '@angular/core';

const maxGalleryThumbnailSize = 7; // TODO: configure in settings

export enum GalleryViewType {
  Grid,
  List
}

@Injectable({
  providedIn: 'root'
})
export class AppStateService {
  private readonly isSidebarOpen = signal<boolean>(true);
  private readonly galleryThumbnailSize = signal<number>(3);
  private readonly galleryViewType = signal<GalleryViewType>(GalleryViewType.List);

  getIsSidebarOpen() {
    return this.isSidebarOpen()
  }

  getGalleryThumbnailSize() {
    return this.galleryThumbnailSize()
  }

  getGalleryViewType() {
    return this.galleryViewType()
  }

  toggleSidebar() {
    this.isSidebarOpen.update(prev => !prev);
  }

  increaseMainGridColumns() {
    this.galleryThumbnailSize.update(prev => prev < maxGalleryThumbnailSize ? prev + 1 : prev);
  }

  decreaseMainGridColumns() {
    this.galleryThumbnailSize.update(prev => prev > 1 ? prev - 1 : prev);
  }

  setGridGalleryView() {
    this.galleryViewType.update(() => GalleryViewType.Grid);
  }

  setListGalleryView() {
    this.galleryViewType.update(() => GalleryViewType.List);
  }
}
