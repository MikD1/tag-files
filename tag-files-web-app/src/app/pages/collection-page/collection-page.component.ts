import {ChangeDetectionStrategy, Component, inject, signal} from '@angular/core';
import {ActivatedRoute, Router} from '@angular/router';
import {CommonModule} from '@angular/common';
import {ImageGridComponent} from '../../components/image-grid/image-grid.component';
import {LibraryItem, LibraryItemPaginatedList} from '../../services/api/library-api.service';
import {
  LibraryCollectionsApiService,
  LibraryCollectionWithItems
} from '../../services/api/library-collections-api.service';
import {MatButtonModule} from '@angular/material/button';
import {MatDialog} from '@angular/material/dialog';
import {CollectionEditModalComponent} from '../collection-edit-modal/collection-edit-modal.component';

@Component({
  selector: 'app-collection-page',
  standalone: true,
  imports: [CommonModule, ImageGridComponent, MatButtonModule],
  templateUrl: './collection-page.component.html',
  styleUrl: './collection-page.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class CollectionPageComponent {
  protected readonly collection = signal<LibraryCollectionWithItems | null>(null);
  private readonly route = inject(ActivatedRoute);
  private readonly libraryCollectionsApi = inject(LibraryCollectionsApiService);
  private readonly dialog = inject(MatDialog);
  private readonly router = inject(Router);

  constructor() {
    this.route.paramMap.subscribe(params => {
      const id = Number(params.get('id'));
      if (id) {
        this.loadCollection(id);
      }
    });
  }

  protected makeItemsPaginatedList(items: LibraryItem[]): LibraryItemPaginatedList {
    return {
      items,
      totalItems: items.length,
      pageIndex: 0,
      totalPages: 1,
    };
  }

  protected editCollection() {
    const dialogRef = this.dialog.open(CollectionEditModalComponent, {
      data: {
        collection: this.collection()!
      },
      width: '600px',
    });

    dialogRef.afterClosed().subscribe((result) => {
      if (result === true) {
        const id = this.collection()?.id;
        if (id) {
          this.loadCollection(id);
        } else {
          this.router.navigate(['/collections']);
        }
      }
    });
  }

  private loadCollection(id: number) {
    this.collection.set(null);
    this.libraryCollectionsApi.getCollection(id).subscribe({
      next: col => this.collection.set(col),
      error: () => this.router.navigate(['/collections'])
    });
  }
}
