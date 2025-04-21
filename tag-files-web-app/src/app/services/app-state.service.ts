import { Injectable, signal } from '@angular/core';

@Injectable({
  providedIn: 'root'
})
export class AppStateService {
  getIsSidebarOpen() {
    return this.isSidebarOpen()
  }

  getMainGridColumns() {
    return this.mainGridColumns()
  }

  toggleSidebar() {
    this.isSidebarOpen.update(prev => !prev);
  }


  increaseMainGridColumns() {
    this.mainGridColumns.update(prev => prev < 12 ? prev + 1 : prev);
  }

  decreaseMainGridColumns() {
    this.mainGridColumns.update(prev => prev > 4 ? prev - 1 : prev);
  }

  private readonly isSidebarOpen = signal<boolean>(true);
  private readonly mainGridColumns = signal<number>(8);
}
