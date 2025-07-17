import {ChangeDetectionStrategy, Component, computed, effect, inject, model, signal} from '@angular/core';
import {COMMA, ENTER} from '@angular/cdk/keycodes';
import {TagsApiService} from '../../services/api/tags-api.service';
import {MatChipInputEvent, MatChipsModule} from '@angular/material/chips';
import {MatAutocompleteModule, MatAutocompleteSelectedEvent} from '@angular/material/autocomplete';
import {FormsModule} from '@angular/forms';
import {MatIconModule} from '@angular/material/icon';
import {MatFormFieldModule} from '@angular/material/form-field';

@Component({
  selector: 'app-tags-form',
  imports: [
    MatIconModule,
    MatFormFieldModule,
    MatChipsModule,
    MatAutocompleteModule,
    FormsModule
  ],
  templateUrl: './tags-form.component.html',
  styleUrl: './tags-form.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class TagsFormComponent {
  public readonly tags = model<string[]>([]);
  protected readonly separatorKeysCodes: number[] = [ENTER, COMMA];
  protected readonly allTags = signal<string[]>([]);
  protected currentTag = model('');
  protected readonly filteredTags = computed(() => {
    const currentTag = this.currentTag().toLowerCase();
    return this.allTags()
      .filter(tag => !this.tags().includes(tag))
      .filter(tag => tag.toLowerCase().includes(currentTag));
  });
  private readonly tagsService = inject(TagsApiService);

  constructor() {
    effect(() => {
      this.tagsService.getTags().subscribe((result) => {
        this.allTags.set(result);
      });
    });
  }

  protected addTag(event: MatChipInputEvent): void {
    const value = (event.value || '').trim();
    if (value && !this.tags().includes(value) && this.allTags().includes(value)) {
      this.tags.update(tags => [...tags, value]);
    }

    this.currentTag.set('');
  }

  protected removeTag(tag: string) {
    this.tags.update(tags => {
      const index = tags.indexOf(tag);
      if (index < 0) {
        return tags;
      }

      tags.splice(index, 1);
      return [...tags];
    });
  }

  protected selectedTag(event: MatAutocompleteSelectedEvent) {
    const selectedValue = event.option.viewValue;
    if (!this.tags().includes(selectedValue)) {
      this.tags.update(tags => [...tags, selectedValue]);
    }

    this.currentTag.set('');
    event.option.deselect();
  }
}
