import {Injectable, signal} from '@angular/core';

@Injectable({providedIn: 'root'})
export class NavigationContextService {
  private readonly itemIds = signal<number[]>([]);

  setContext(ids: number[]): void {
    this.itemIds.set(ids);
  }

  getPrevId(currentId: number): number | null {
    const ids = this.itemIds();
    const index = ids.indexOf(currentId);
    return index > 0 ? ids[index - 1] : null;
  }

  getNextId(currentId: number): number | null {
    const ids = this.itemIds();
    const index = ids.indexOf(currentId);
    return index >= 0 && index < ids.length - 1 ? ids[index + 1] : null;
  }
}
