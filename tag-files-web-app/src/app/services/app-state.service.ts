import {Injectable, signal} from '@angular/core';

const maxGalleryThumbnailSize = 7; // TODO: configure in settings

export enum GalleryViewType {
  Grid = 'grid',
  List = 'list'
}

const localStorageKeys = {
  isSidebarOpen: 'appState.isSidebarOpen',
  galleryThumbnailSize: 'appState.galleryThumbnailSize',
  galleryViewType: 'appState.galleryViewType',
};

@Injectable({
  providedIn: 'root'
})
export class AppStateService {
  private readonly isSidebarOpen = signal<boolean>(this.loadFromLocalStorage(localStorageKeys.isSidebarOpen, true));
  private readonly galleryThumbnailSize = signal<number>(this.loadFromLocalStorage(localStorageKeys.galleryThumbnailSize, 3));
  private readonly galleryViewType = signal<GalleryViewType>(this.loadFromLocalStorage(localStorageKeys.galleryViewType, GalleryViewType.Grid));

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
    this.isSidebarOpen.update(prev => {
      const newValue = !prev;
      this.saveToLocalStorage(localStorageKeys.isSidebarOpen, newValue);
      return newValue;
    });
  }

  increaseMainGridColumns() {
    this.galleryThumbnailSize.update(prev => {
      const newValue = prev < maxGalleryThumbnailSize ? prev + 1 : prev;
      this.saveToLocalStorage(localStorageKeys.galleryThumbnailSize, newValue);
      return newValue;
    });
  }

  decreaseMainGridColumns() {
    this.galleryThumbnailSize.update(prev => {
      const newValue = prev > 1 ? prev - 1 : prev;
      this.saveToLocalStorage(localStorageKeys.galleryThumbnailSize, newValue);
      return newValue;
    });
  }

  setGridGalleryView() {
    this.galleryViewType.update(() => {
      this.saveToLocalStorage(localStorageKeys.galleryViewType, GalleryViewType.Grid);
      return GalleryViewType.Grid;
    });
  }

  setListGalleryView() {
    this.galleryViewType.update(() => {
      this.saveToLocalStorage(localStorageKeys.galleryViewType, GalleryViewType.List);
      return GalleryViewType.List;
    });
  }

  private saveToLocalStorage<T>(key: string, value: T): void {
    localStorage.setItem(key, JSON.stringify(value));
  }

  private loadFromLocalStorage<T>(key: string, defaultValue: T): T {
    const storedValue = localStorage.getItem(key);
    return storedValue !== null ? JSON.parse(storedValue) : defaultValue;
  }
}
