import {Component, effect, inject, signal} from '@angular/core';
import {ActivatedRoute} from '@angular/router';
import {LibraryApiService, LibraryItem} from '../../services/api/library-api.service';
import {toSignal} from '@angular/core/rxjs-interop';
import {map} from 'rxjs';

@Component({
  selector: 'app-library-item-edit-page',
  imports: [],
  templateUrl: './library-item-edit-page.component.html',
  styleUrl: './library-item-edit-page.component.scss'
})
export class LibraryItemEditPageComponent {
  protected readonly item = signal<LibraryItem>(null!);
  private readonly route = inject(ActivatedRoute);
  private readonly libraryService = inject(LibraryApiService);
  private itemId = toSignal(this.route.paramMap.pipe(
    map(params => Number(params.get('id')))
  ));

  constructor() {
    effect(() => {
      const id = this.itemId();
      if (id == undefined || isNaN(id)) {
        return;
      }

      this.libraryService.getItem(id).subscribe((result) => {
        this.item.set(result);
      });
    });
  }
}
