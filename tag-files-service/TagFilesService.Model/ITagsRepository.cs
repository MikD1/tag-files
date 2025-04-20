namespace TagFilesService.Model;

public interface ITagsRepository
{
    Task<Tag> SaveTag(Tag tag);

    Task<List<Tag>> GetTags();

    Task DeleteTag(uint id);
}