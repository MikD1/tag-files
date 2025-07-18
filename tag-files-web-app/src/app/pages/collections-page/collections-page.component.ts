import {ChangeDetectionStrategy, Component, effect, inject, signal} from '@angular/core';
import {LibraryCollection, LibraryCollectionsApiService} from '../../services/api/library-collections-api.service';
import {MatTableModule} from '@angular/material/table';
import {MatButtonModule} from '@angular/material/button';
import {MatChipsModule} from '@angular/material/chips';
import {MatListModule} from '@angular/material/list';
import {MatIconModule} from '@angular/material/icon';
import {MatDialog} from '@angular/material/dialog';
import {CollectionEditModalComponent} from '../collection-edit-modal/collection-edit-modal.component';

@Component({
  selector: 'app-collections-table',
  standalone: true,
  imports: [
    MatTableModule,
    MatButtonModule,
    MatChipsModule,
    MatListModule,
    MatIconModule
  ],
  templateUrl: './collections-page.component.html',
  styleUrl: './collections-page.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class CollectionsPageComponent {
  protected readonly collections = signal<LibraryCollection[]>([]);
  protected readonly displayedColumns = ['name', 'actions'];
  private readonly collectionsService = inject(LibraryCollectionsApiService);
  private dialog = inject(MatDialog);

  constructor() {
    effect(() => {
      this.loadCollections();
    });
  }

  protected editItem(collection: LibraryCollection) {
    const dialogRef = this.dialog.open(CollectionEditModalComponent, {
      data: {
        collection: collection,
      },
      width: '600px',
    });

    dialogRef.afterClosed().subscribe((result) => {
      if (result === true) {
        this.loadCollections();
      }
    });
  }

  protected addCollection() {
    const dialogRef = this.dialog.open(CollectionEditModalComponent, {
      data: {
        collection: null,
      },
      width: '600px',
    });

    dialogRef.afterClosed().subscribe((result) => {
      if (result === true) {
        this.loadCollections();
      }
    });
  }

  private loadCollections() {
    this.collectionsService.getCollections().subscribe((result) => {
      this.collections.set(result);
    });
  }
}
