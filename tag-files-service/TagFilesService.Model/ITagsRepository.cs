namespace TagFilesService.Model;

public interface ITagsRepository
{
    Task<Tag> SaveTag(Tag tag);

    Task<List<Tag>> GetTags();

    Task<Tag> GetTag(string name);

    Task DeleteTag(string name);
}