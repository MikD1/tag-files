import {ChangeDetectionStrategy, Component, effect, inject, model, signal} from '@angular/core';
import {CommonModule} from '@angular/common';
import {MatFormFieldModule} from '@angular/material/form-field';
import {MatSelectModule} from '@angular/material/select';
import {FormsModule} from '@angular/forms';
import {LibraryCollection, LibraryCollectionsApiService} from '../../services/api/library-collections-api.service';

@Component({
  selector: 'app-select-collection',
  standalone: true,
  imports: [CommonModule, MatFormFieldModule, MatSelectModule, FormsModule],
  templateUrl: './select-collection.component.html',
  styleUrl: './select-collection.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SelectCollectionComponent {
  public readonly collectionId = model<number | null>(null);
  protected readonly allCollections = signal<LibraryCollection[]>([]);
  private readonly collectionsService = inject(LibraryCollectionsApiService);

  constructor() {
    effect(() => {
      this.collectionsService.getCollections().subscribe((result) => {
        this.allCollections.set(result);
      });
    });
  }
}
